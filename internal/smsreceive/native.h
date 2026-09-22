#ifndef SMSRECEIVE_NATIVE_H
#define SMSRECEIVE_NATIVE_H
#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/IOKitLib.h>
#include <IOKit/IOCFPlugIn.h>
#include <IOKit/usb/IOUSBLib.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

// New code: intentionally no libusb, device open, seize, control requests,
// line-state setup, reset, stall clearing, alternate/configuration changes.
#include <stdio.h>
#define G1_EVENT(s) do { fprintf(stderr,"smsreceive:%s\n",s); fflush(stderr); } while(0)

typedef unsigned char uchar;
typedef unsigned int uint;
typedef struct {
 io_service_t parent;
 IOUSBDeviceInterface **device;
 IOUSBInterfaceInterface **iface;
 int opened;
 uint write_attempts;
 int write_failed;
 uint read_attempts;
 UInt8 in_pipe, out_pipe;
 uint raw_len;
 uchar raw[65535];
} g1_port;

static int g1_prop(io_service_t s,CFStringRef key,int64_t want) {
 CFTypeRef v=IORegistryEntryCreateCFProperty(s,key,kCFAllocatorDefault,0);
 if (!v) return 0;
 int64_t n=-1;
 int ok=CFGetTypeID(v)==CFNumberGetTypeID() && CFNumberGetValue((CFNumberRef)v,kCFNumberSInt64Type,&n) && n==want;
 CFRelease(v);return ok;
}
static int g1_parent_ok(io_service_t s) {
 return g1_prop(s,CFSTR("idVendor"),0x2ca3) && g1_prop(s,CFSTR("idProduct"),0x4006) &&
  g1_prop(s,CFSTR("locationID"),0x100000) && g1_prop(s,CFSTR("bcdDevice"),792) &&
  g1_prop(s,CFSTR("kUSBCurrentConfiguration"),1);
}
static int g1_interface_ok(io_service_t s) {
 return IOObjectConformsTo(s,"IOUSBHostInterface") &&
  g1_prop(s,CFSTR("bInterfaceNumber"),2) && g1_prop(s,CFSTR("bInterfaceClass"),255) &&
  g1_prop(s,CFSTR("bInterfaceSubClass"),0) && g1_prop(s,CFSTR("bInterfaceProtocol"),0) &&
  g1_prop(s,CFSTR("bNumEndpoints"),3);
}
static IOReturn g1_close(g1_port *p) {
 if (!p) return kIOReturnSuccess;
 IOReturn rc=kIOReturnSuccess;
 if (p->iface) {
  if (p->opened) { G1_EVENT("interface_2_close_attempt"); rc=(*p->iface)->USBInterfaceClose(p->iface); G1_EVENT(rc ? "interface_2_close_error" : "interface_2_close_ok"); }
  (*p->iface)->Release(p->iface);
 }
 if (p->device) (*p->device)->Release(p->device);
 if (p->parent) IOObjectRelease(p->parent);
 free(p);return rc;
}
static IOReturn g1_composition(g1_port *p) {
 if (!g1_parent_ok(p->parent)) return kIOReturnNotPermitted;
 UInt8 count=0;
 IOReturn rc=(*p->device)->GetNumberOfConfigurations(p->device,&count);
 if (rc || count<1 || count>8) return kIOReturnNotPermitted;
 int found=0;
 for (UInt8 i=0;i<count;i++) {
  IOUSBConfigurationDescriptorPtr d=NULL;
  rc=(*p->device)->GetConfigurationDescriptorPtr(p->device,i,&d);
  if (rc || !d) return kIOReturnNotPermitted;
  const uchar *b=(const uchar *)d;
  if (b[0]<9 || b[1]!=2) return kIOReturnNotPermitted;
  if (b[5]!=1) continue;
  uint len=(uint)b[2] | ((uint)b[3]<<8);
  if (++found!=1 || len!=p->raw_len || memcmp(b,p->raw,len)) return kIOReturnNotPermitted;
 }
 return found==1 ? kIOReturnSuccess : kIOReturnNotPermitted;
}
static IOReturn g1_pipes(g1_port *p) {
 UInt8 v=0;
 if ((*p->iface)->GetInterfaceNumber(p->iface,&v) || v!=2) return kIOReturnNotPermitted;
 if ((*p->iface)->GetAlternateSetting(p->iface,&v) || v!=0) return kIOReturnNotPermitted;
 if ((*p->iface)->GetConfigurationValue(p->iface,&v) || v!=1) return kIOReturnNotPermitted;
 if ((*p->iface)->GetInterfaceClass(p->iface,&v) || v!=255) return kIOReturnNotPermitted;
 if ((*p->iface)->GetInterfaceSubClass(p->iface,&v) || v!=0) return kIOReturnNotPermitted;
 if ((*p->iface)->GetInterfaceProtocol(p->iface,&v) || v!=0) return kIOReturnNotPermitted;
 if ((*p->iface)->GetNumEndpoints(p->iface,&v) || v!=3) return kIOReturnNotPermitted;
 unsigned seen=0;
 UInt8 in=0,out=0;
 for (UInt8 pipe=1;pipe<=3;pipe++) {
  UInt8 direction=0,number=0,type=0,interval=0; UInt16 size=0;
  IOReturn rc=(*p->iface)->GetPipeProperties(p->iface,pipe,&direction,&number,&type,&size,&interval);
  if (rc) return rc;
  unsigned bit=0;
  if (direction==kUSBIn && number==4 && type==kUSBBulk && size==512 && interval==0) {bit=1;in=pipe;}
  if (direction==kUSBOut && number==3 && type==kUSBBulk && size==512 && interval==0) {bit=2;out=pipe;}
  if (direction==kUSBIn && number==5 && type==kUSBInterrupt && size==10 && interval==9) bit=4;
  if (!bit || (seen&bit)) return kIOReturnNotPermitted;
  seen|=bit;
 }
 if (seen!=7) return kIOReturnNotPermitted;
 // Mapping cannot change after it has been accepted.
 if ((p->in_pipe && p->in_pipe!=in) || (p->out_pipe && p->out_pipe!=out)) return kIOReturnNotPermitted;
 p->in_pipe=in;p->out_pipe=out;return kIOReturnSuccess;
}
static IOReturn g1_open(const uchar *raw,uint len,g1_port **out) {
 if (!out || *out || !raw || len<9 || len>65535) return kIOReturnBadArgument;
 g1_port *p=calloc(1,sizeof(*p)); if (!p) return kIOReturnNoMemory;
 memcpy(p->raw,raw,len);p->raw_len=len;
 io_iterator_t iter=0;
 IOReturn rc=IOServiceGetMatchingServices(kIOMainPortDefault,IOServiceMatching("IOUSBHostDevice"),&iter);
 if (rc) {g1_close(p);return rc;}
 io_service_t s;int matches=0;
 while ((s=IOIteratorNext(iter))) {
  if (g1_parent_ok(s)) {matches++;if (!p->parent) {p->parent=s;IOObjectRetain(s);}}
  IOObjectRelease(s);
 }
 IOObjectRelease(iter);
 if (matches!=1) {g1_close(p);return kIOReturnNotPermitted;}
 IOCFPlugInInterface **plugin=NULL;SInt32 score=0;
 G1_EVENT("device_cached_descriptor_plugin");
 rc=IOCreatePlugInInterfaceForService(p->parent,kIOUSBDeviceUserClientTypeID,kIOCFPlugInInterfaceID,&plugin,&score);
 if (rc || !plugin) {if(plugin)(*plugin)->Release(plugin);g1_close(p);return rc?rc:kIOReturnError;}
 HRESULT hr=(*plugin)->QueryInterface(plugin,CFUUIDGetUUIDBytes(kIOUSBDeviceInterfaceID),(LPVOID *)&p->device);
 (*plugin)->Release(plugin);
 if (hr || !p->device) {g1_close(p);return kIOReturnNotPermitted;}
 rc=g1_composition(p);
 if (rc) {g1_close(p);return rc;}
 rc=IORegistryEntryGetChildIterator(p->parent,kIOServicePlane,&iter);
 if (rc) {g1_close(p);return rc;}
 io_service_t selected=0;matches=0;
 while ((s=IOIteratorNext(iter))) {
  if (g1_interface_ok(s)) {matches++;if (!selected) {selected=s;IOObjectRetain(s);}}
  IOObjectRelease(s);
 }
 IOObjectRelease(iter);
 if (matches!=1) {if(selected)IOObjectRelease(selected);g1_close(p);return kIOReturnNotPermitted;}
 plugin=NULL;
 G1_EVENT("interface_2_plugin");
 rc=IOCreatePlugInInterfaceForService(selected,kIOUSBInterfaceUserClientTypeID,kIOCFPlugInInterfaceID,&plugin,&score);
 IOObjectRelease(selected);
 if (rc || !plugin) {if(plugin)(*plugin)->Release(plugin);g1_close(p);return rc?rc:kIOReturnError;}
 hr=(*plugin)->QueryInterface(plugin,CFUUIDGetUUIDBytes(kIOUSBInterfaceInterfaceID),(LPVOID *)&p->iface);
 (*plugin)->Release(plugin);
 if (hr || !p->iface) {g1_close(p);return kIOReturnNotPermitted;}
 // Non-seizing acquisition of precisely the selected interface; never retry.
 G1_EVENT("interface_2_open_attempt");
 rc=(*p->iface)->USBInterfaceOpen(p->iface);
 if (rc) {g1_close(p);return rc;}
 p->opened=1;G1_EVENT("interface_2_open_ok");
 rc=g1_composition(p);if (!rc) rc=g1_pipes(p);
 if (rc) {g1_close(p);return rc;}
 *out=p;return kIOReturnSuccess;
}
static IOReturn g1_query(g1_port *p,uint query,uint ms) {
 if (!p || !p->opened || !ms || ms>250) return kIOReturnBadArgument;
 const char *wire=NULL;
 switch(query) {
 case 1: wire="AT+CMGF?\r";break;
 case 2: wire="AT+CPMS?\r";break;
 case 3: wire="AT+CNMI?\r";break;
 case 4: wire="AT+CMGL=4\r";break;
 case 5: wire="AT+CPMS?\r";break;
 case 6: wire="AT+CMGF?\r";break;
 case 7: wire="AT+CNMI?\r";break;
 default: return kIOReturnNotPermitted;
 }
 // Exact immutable plan; no duplicate, skipped, caller-supplied or retried write.
 if (p->write_failed || query!=p->write_attempts+1) return kIOReturnNotPermitted;
 IOReturn rc=g1_composition(p);if (!rc) rc=g1_pipes(p);
 if (rc) return rc;
 p->write_attempts++;G1_EVENT("readonly_write_attempt");
 rc=(*p->iface)->WritePipeTO(p->iface,p->out_pipe,(void *)wire,(UInt32)strlen(wire),ms,ms);
 if (rc) p->write_failed=1;
 G1_EVENT(rc ? "readonly_write_error" : "readonly_write_ok");return rc;
}
static IOReturn g1_read(g1_port *p,void *buf,uint *len,uint ms) {
 if (!p || !p->opened || !buf || !len || *len>512 || !ms || ms>250) return kIOReturnBadArgument;
 p->read_attempts++;G1_EVENT("interface_2_read_attempt");
 UInt32 n=*len;
 IOReturn rc=(*p->iface)->ReadPipeTO(p->iface,p->in_pipe,buf,&n,ms,ms);
 *len=n;return rc;
}
#endif
