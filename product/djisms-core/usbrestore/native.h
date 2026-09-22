#ifndef DJISMS_USBRESTORE_H
#define DJISMS_USBRESTORE_H
#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/IOKitLib.h>
#include <IOKit/IOCFPlugIn.h>
#include <IOKit/usb/IOUSBLib.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>
#include <fcntl.h>
#include <stdint.h>
#include "profile.h"
typedef struct {io_service_t service; IOUSBDeviceInterface **usb; uint64_t registry; uint32_t location;} Context;
static int prop(io_service_t s,CFStringRef k,int64_t want){CFTypeRef v=IORegistryEntryCreateCFProperty(s,k,kCFAllocatorDefault,0);int64_t n=-1;int ok=v&&CFGetTypeID(v)==CFNumberGetTypeID()&&CFNumberGetValue(v,kCFNumberSInt64Type,&n)&&n==want;if(v)CFRelease(v);return ok;}
static int identity(Context*c){uint64_t rid=0;return !IORegistryEntryGetRegistryEntryID(c->service,&rid)&&rid==c->registry&&prop(c->service,CFSTR("idVendor"),0x2ca3)&&prop(c->service,CFSTR("idProduct"),0x4006)&&prop(c->service,CFSTR("bcdDevice"),792)&&prop(c->service,CFSTR("locationID"),c->location)&&prop(c->service,CFSTR("kUSBCurrentConfiguration"),1);}
static int descriptor(Context*c){UInt8 count=0;if((*c->usb)->GetNumberOfConfigurations(c->usb,&count)||count!=1)return 10;IOUSBConfigurationDescriptorPtr d=NULL;if((*c->usb)->GetConfigurationDescriptorPtr(c->usb,0,&d)||!d)return 11;const unsigned char*b=(void*)d;unsigned len=b[2]|((unsigned)b[3]<<8);if(len!=sizeof(profile)||memcmp(b,profile,len))return 12;return 0;}
static int interfaces(Context*c){io_iterator_t it=0;if(IORegistryEntryGetChildIterator(c->service,kIOServicePlane,&it))return 26;io_service_t s;unsigned seen=0;int bad=0;while((s=IOIteratorNext(it))){if(IOObjectConformsTo(s,"IOUSBHostInterface")){int n=-1;for(int i=0;i<6;i++)if(prop(s,CFSTR("bInterfaceNumber"),i))n=i;if(n<0||(seen&(1U<<n))){bad=1;}else{seen|=1U<<n;io_iterator_t children=0;if(IORegistryEntryGetChildIterator(s,kIOServicePlane,&children)){bad=1;}else{io_service_t child;unsigned owners=0;while((child=IOIteratorNext(children))){owners++;io_name_t name={0};IORegistryEntryGetName(child,name);if(n<4||(n==4&&strcmp(name,"AppleUserECM"))||(n==5&&strcmp(name,"AppleUserECMData")))bad=1;IOObjectRelease(child);}if(n>=4&&owners!=1)bad=1;IOObjectRelease(children);}}}IOObjectRelease(s);}IOObjectRelease(it);return bad||seen!=63?27:0;}
static int unique_target(Context*c){io_iterator_t it=0;if(IOServiceGetMatchingServices(kIOMainPortDefault,IOServiceMatching("IOUSBHostDevice"),&it))return 29;io_service_t s;unsigned count=0;int target=0;while((s=IOIteratorNext(it))){if(prop(s,CFSTR("idVendor"),0x2ca3)&&prop(s,CFSTR("idProduct"),0x4006)){count++;uint64_t id=0;if(!IORegistryEntryGetRegistryEntryID(s,&id)&&id==c->registry)target=1;}IOObjectRelease(s);}IOObjectRelease(it);return count==1&&target?0:29;}
static int recheck(void*v){Context*c=v;if(!identity(c))return 13;int r=unique_target(c);if(r)return r;r=descriptor(c);return r?r:interfaces(c);}
static int prepare(void*v){Context*c=v;io_iterator_t it=0;if(geteuid()==0||!c->registry)return 14;if(IOServiceGetMatchingServices(kIOMainPortDefault,IOServiceMatching("IOUSBHostDevice"),&it))return 15;io_service_t s;unsigned matches=0;while((s=IOIteratorNext(it))){if(prop(s,CFSTR("idVendor"),0x2ca3)&&prop(s,CFSTR("idProduct"),0x4006)){matches++;uint64_t id=0;if(!IORegistryEntryGetRegistryEntryID(s,&id)&&id==c->registry){c->service=s;IOObjectRetain(s);}}IOObjectRelease(s);}IOObjectRelease(it);if(matches!=1||!c->service||!identity(c))return 16;
IOCFPlugInInterface **plug=NULL;SInt32 score=0;IOReturn r=IOCreatePlugInInterfaceForService(c->service,kIOUSBDeviceUserClientTypeID,kIOCFPlugInInterfaceID,&plug,&score);if(r||!plug){if(plug)IODestroyPlugInInterface(plug);return 17;}HRESULT h=(*plug)->QueryInterface(plug,CFUUIDGetUUIDBytes(kIOUSBDeviceInterfaceID),(LPVOID*)&c->usb);IODestroyPlugInInterface(plug);if(h||!c->usb)return 18;return recheck(c);}

typedef struct { int stage; IOReturn open_rc, reenumerate_rc, close_rc; int attempted; } restore_result;
static Context *restore_prepare(uint64_t registry,uint32_t location,int *error) {
 Context*c=calloc(1,sizeof(*c));if(!c){*error=28;return NULL;}
 c->registry=registry;c->location=location;*error=prepare(c);return c;
}
static void restore_release(Context*c){if(!c)return;if(c->usb)(*c->usb)->Release(c->usb);if(c->service)IOObjectRelease(c->service);free(c);}
static restore_result restore_once(Context*c){
 restore_result r={0};r.stage=recheck(c);if(r.stage)return r;
 r.open_rc=(*c->usb)->USBDeviceOpen(c->usb);if(r.open_rc){r.stage=30;return r;}
 r.stage=recheck(c);
 if(!r.stage){r.attempted=1;r.reenumerate_rc=(*c->usb)->USBDeviceReEnumerate(c->usb,0);if(r.reenumerate_rc)r.stage=31;}
 r.close_rc=(*c->usb)->USBDeviceClose(c->usb);
 if(!r.stage&&r.close_rc&&r.close_rc!=kIOReturnNoDevice)r.stage=32;
 return r;
}

#endif
