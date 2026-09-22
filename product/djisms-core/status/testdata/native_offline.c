// Offline harness: every IOKit service call is replaced. This executable links
// CoreFoundation only, NOT IOKit, so it cannot reach USB enumeration or hardware.
#include <assert.h>
#include <stdio.h>
#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/IOKitLib.h>
#include <IOKit/IOCFPlugIn.h>
#include <IOKit/usb/IOUSBLib.h>

static CFTypeRef fake_property(io_registry_entry_t,CFStringRef,CFAllocatorRef,IOOptionBits);
static kern_return_t fake_services(mach_port_t,CFDictionaryRef,io_iterator_t*);
static CFMutableDictionaryRef fake_matching(const char*);
static io_object_t fake_next(io_iterator_t);
static kern_return_t fake_release(io_object_t);
static kern_return_t fake_retain(io_object_t);
static boolean_t fake_conforms(io_object_t,const io_name_t);
static kern_return_t fake_children(io_registry_entry_t,const io_name_t,io_iterator_t*);
static IOReturn fake_plugin(io_service_t,CFUUIDRef,CFUUIDRef,IOCFPlugInInterface***,SInt32*);
static kern_return_t fake_registry_id(io_registry_entry_t s,uint64_t *out) { (void)s; *out=42; return KERN_SUCCESS; }
#define IORegistryEntryGetRegistryEntryID fake_registry_id
#define IORegistryEntryCreateCFProperty fake_property
#define IOServiceGetMatchingServices fake_services
#define IOServiceMatching fake_matching
#define IOIteratorNext fake_next
#define IOObjectRelease fake_release
#define IOObjectRetain fake_retain
#define IOObjectConformsTo fake_conforms
#define IORegistryEntryGetChildIterator fake_children
#define IOCreatePlugInInterfaceForService fake_plugin
#define kIOMainPortDefault 0
#include "../native.h"

static uchar descriptor[65535];static uint descriptor_len;
static int devices,children,device_cursor,child_cursor,selected_number,opened,closed,writes,reads,mode,plugin_kind;
static int wrong_pipe,wrong_alt,config_changed,busy,write_fail,device_releases,iface_releases;
static int read_mode=-1;
static UInt8 written_pipe;static char written[64];
static IOUSBDeviceInterface device_table,*device_ptr=&device_table;
static IOUSBInterfaceInterface interface_table,*interface_ptr=&interface_table;
static IOCFPlugInInterface plugin_table,*plugin_ptr=&plugin_table;
static CFTypeRef fake_property(io_registry_entry_t s,CFStringRef key,CFAllocatorRef alloc,IOOptionBits options){
 (void)alloc;(void)options;
 int64_t n=-1;
 if (s>=100 && s<200) {
  if(CFEqual(key,CFSTR("idVendor")))n=0x2ca3;
  if(CFEqual(key,CFSTR("idProduct")))n=0x4006;
  if(CFEqual(key,CFSTR("locationID")))n=0;
  if(CFEqual(key,CFSTR("bcdDevice")))n=792;
  if(CFEqual(key,CFSTR("kUSBCurrentConfiguration")))n=config_changed?2:1;
 } else {
  if(CFEqual(key,CFSTR("bInterfaceNumber")))n=(s==202 || s==206)?2:s-200;
  if(CFEqual(key,CFSTR("bInterfaceClass")))n=s==204?2:(s==205?10:255);
  if(CFEqual(key,CFSTR("bInterfaceSubClass")))n=0;
  if(CFEqual(key,CFSTR("bInterfaceProtocol")))n=0;
  if(CFEqual(key,CFSTR("bNumEndpoints")))n=3;
 }
 return CFNumberCreate(kCFAllocatorDefault,kCFNumberSInt64Type,&n);
}
static CFMutableDictionaryRef fake_matching(const char*n){assert(!strcmp(n,"IOUSBHostDevice"));return NULL;}
static kern_return_t fake_services(mach_port_t p,CFDictionaryRef d,io_iterator_t*i){(void)p;(void)d;device_cursor=0;*i=1;return 0;}
static io_object_t fake_next(io_iterator_t i){if(i==1)return device_cursor<devices?100+device_cursor++:0;return child_cursor<children?200+child_cursor++:0;}
static kern_return_t fake_release(io_object_t s){(void)s;return 0;}
static kern_return_t fake_retain(io_object_t s){(void)s;return 0;}
static boolean_t fake_conforms(io_object_t s,const io_name_t n){assert(!strcmp(n,"IOUSBHostInterface"));return s>=200;}
static kern_return_t fake_children(io_registry_entry_t s,const io_name_t p,io_iterator_t*i){assert(s==100);(void)p;child_cursor=0;*i=2;return 0;}
static ULONG fake_com_release(void*self){if(self==&device_ptr)device_releases++;if(self==&interface_ptr)iface_releases++;return 0;}
static HRESULT fake_query(void*self,REFIID id,LPVOID*out){(void)self;(void)id;*out=plugin_kind==1?(void*)&device_ptr:(void*)&interface_ptr;return 0;}
static IOReturn fake_plugin(io_service_t s,CFUUIDRef a,CFUUIDRef b,IOCFPlugInInterface***out,SInt32*score){
 (void)a;(void)b;*score=1;plugin_kind=s<200?1:2;
 if(s>=200){selected_number=(s==206)?2:s-200;assert(selected_number==2);}
 *out=&plugin_ptr;return 0;
}
static IOReturn fake_config_count(void*self,UInt8*n){(void)self;*n=1;return 0;}
static IOReturn fake_descriptor(void*self,UInt8 i,IOUSBConfigurationDescriptorPtr*d){(void)self;assert(i==0);*d=(IOUSBConfigurationDescriptorPtr)descriptor;return 0;}
static IOReturn fake_open(void*self){(void)self;opened++;return busy?kIOReturnExclusiveAccess:0;}
static IOReturn fake_close(void*self){(void)self;closed++;return 0;}
static IOReturn fake_number(void*self,UInt8*n){(void)self;*n=selected_number;return 0;}
static IOReturn fake_alt(void*self,UInt8*n){(void)self;*n=wrong_alt;return 0;}
static IOReturn fake_config(void*self,UInt8*n){(void)self;*n=1;return 0;}
static IOReturn fake_class(void*self,UInt8*n){(void)self;*n=255;return 0;}
static IOReturn fake_zero(void*self,UInt8*n){(void)self;*n=0;return 0;}
static IOReturn fake_eps(void*self,UInt8*n){(void)self;*n=3;return 0;}
static IOReturn fake_pipe(void*self,UInt8 pipe,UInt8*dir,UInt8*num,UInt8*type,UInt16*size,UInt8*interval){
 (void)self;assert(pipe>=1 && pipe<=3);*interval=0;*size=512;*type=kUSBBulk;
 // Deliberately shuffled pipe references: OUT=1, interrupt=2, IN=3.
 if(pipe==1){*dir=kUSBOut;*num=wrong_pipe?4:3;}
 if(pipe==2){*dir=kUSBIn;*num=5;*type=kUSBInterrupt;*size=10;*interval=9;}
 if(pipe==3){*dir=kUSBIn;*num=4;}
 return 0;
}
static IOReturn fake_write(void*self,UInt8 pipe,void*buf,UInt32 n,UInt32 a,UInt32 b){
 (void)self;assert(a>0 && a<=250 && a==b && n<sizeof(written));writes++;written_pipe=pipe;memcpy(written,buf,n);written[n]=0;return write_fail?kIOReturnTimeout:0;
}
static IOReturn fake_read(void*self,UInt8 pipe,void*buf,UInt32*n,UInt32 a,UInt32 b){(void)self;(void)buf;assert(pipe==3 && a==b && a>0 && a<=250);reads++;
 if(read_mode==0){memcpy(buf,"abc",3);*n=3;return 0;}
 if(read_mode==1)return kIOReturnTimeout;
 if(read_mode==2)return 0xe0004051;
 if(read_mode==3)return kIOReturnNotResponding;
 if(read_mode==4)return kIOReturnNoDevice;
 *n=0;return kIOReturnTimeout;}
static void setup(void){
 devices=1;children=6;device_cursor=child_cursor=0;selected_number=-1;
 opened=closed=writes=reads=wrong_pipe=wrong_alt=config_changed=busy=write_fail=device_releases=iface_releases=0;
 plugin_table.QueryInterface=fake_query;plugin_table.Release=fake_com_release;
 device_table.Release=fake_com_release;device_table.GetNumberOfConfigurations=fake_config_count;device_table.GetConfigurationDescriptorPtr=fake_descriptor;
 interface_table.Release=fake_com_release;interface_table.USBInterfaceOpen=fake_open;interface_table.USBInterfaceClose=fake_close;
 interface_table.GetInterfaceNumber=fake_number;interface_table.GetAlternateSetting=fake_alt;interface_table.GetConfigurationValue=fake_config;
 interface_table.GetInterfaceClass=fake_class;interface_table.GetInterfaceSubClass=fake_zero;interface_table.GetInterfaceProtocol=fake_zero;
 interface_table.GetNumEndpoints=fake_eps;interface_table.GetPipeProperties=fake_pipe;interface_table.WritePipeTO=fake_write;interface_table.ReadPipeTO=fake_read;
}
int main(int argc,char**argv){
 assert(argc==2);FILE*f=fopen(argv[1],"rb");assert(f);descriptor_len=(uint)fread(descriptor,1,sizeof(descriptor),f);fclose(f);assert(descriptor_len>=9);
 g1_port*p=NULL;setup();assert(g1_open(descriptor,descriptor_len,0,&p));assert(!p && !opened);assert(g1_open(descriptor,descriptor_len,99,&p));assert(!p && !opened);setup();assert(!g1_open(descriptor,descriptor_len,42,&p));assert(opened==1 && writes==0 && reads==0 && selected_number==2);
 assert(p->in_pipe==3 && p->out_pipe==1);
 uchar buffer[512];uint length=sizeof(buffer);assert(g1_read(p,buffer,&length,50)==kIOReturnTimeout && length==0);
 for(uint q=2;q<256;q++)assert(g1_query(p,q,50));assert(g1_query(p,0,50));assert(writes==0);
 assert(g1_query(p,1,0));assert(g1_query(p,1,251));assert(writes==0);
 wrong_pipe=1;assert(g1_query(p,1,50));assert(writes==0);wrong_pipe=0;
 config_changed=1;assert(g1_query(p,1,50));assert(writes==0);config_changed=0;
 const char* expected[]={"AT+CGMI\r","AT+CGMM\r","AT+CGMR\r","AT+CPIN?\r","AT+CSQ\r","AT+CREG?\r","AT+CEREG?\r","AT+COPS?\r","AT+QCCID\r","AT+CNUM\r"};
 for(uint q=1;q<=10;q++){
  assert(!g1_query(p,q,50));assert(writes==(int)q && written_pipe==1 && !strcmp(written,expected[q-1]));
  assert(g1_query(p,q,50));assert(writes==(int)q);
 }
 for(uint q=0;q<256;q++)assert(g1_query(p,q,50));assert(writes==10);
 assert(!g1_close(p));p=NULL;assert(closed==1 && device_releases==1 && iface_releases==1);
 setup();assert(!g1_open(descriptor,descriptor_len,42,&p));write_fail=1;
 assert(g1_query(p,1,50));assert(writes==1);for(uint q=1;q<=10;q++)assert(g1_query(p,q,50));assert(writes==1);
 assert(!g1_close(p));p=NULL;
 for(read_mode=0;read_mode<5;read_mode++){
  setup();assert(!g1_open(descriptor,descriptor_len,42,&p));length=sizeof(buffer);
  IOReturn rc=g1_read(p,buffer,&length,50);
  if(read_mode==0){assert(rc==0 && length==3 && !memcmp(buffer,"abc",3));}else{assert(rc!=0 && length==512);}
  assert(reads==1 && writes==0);assert(!g1_close(p));p=NULL;
 }
 for(int failure=0;failure<7;failure++){
  setup();uchar expected[65535];memcpy(expected,descriptor,descriptor_len);
  if(failure==0)devices=0;
  if(failure==1)devices=2;
  if(failure==2)children=7; // duplicate interface 2: reject, never open either
  if(failure==3)busy=1;
  if(failure==4)wrong_pipe=1;
  if(failure==5)wrong_alt=1;
  if(failure==6)expected[descriptor_len-1]^=1;
  assert(g1_open(expected,descriptor_len,42,&p));assert(!p && !writes && !reads && opened<=1);
  if(failure==0 || failure==1 || failure==2 || failure==6)assert(opened==0);
  if(failure==3)assert(opened==1 && closed==0 && iface_releases==1);
  if(failure==4 || failure==5)assert(closed==1 && iface_releases==1);
 }
 puts("native offline: interface selection, ambiguity, busy, descriptor/pipe/alternate mismatch, query allowlist, timeouts, no retry, cleanup PASS");
 return 0;
}
