// No IOKit linkage. All IOKit functions and COM calls are offline substitutes.
#include <assert.h>
#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/IOKitLib.h>
#include <IOKit/IOCFPlugIn.h>
#include <IOKit/usb/IOUSBLib.h>
#include <unistd.h>
static CFTypeRef fake_property(io_registry_entry_t,CFStringRef,CFAllocatorRef,IOOptionBits);
static kern_return_t fake_services(mach_port_t,CFDictionaryRef,io_iterator_t*);
static CFMutableDictionaryRef fake_matching(const char*);
static io_object_t fake_next(io_iterator_t);
static kern_return_t fake_release(io_object_t);
static kern_return_t fake_retain(io_object_t);
static boolean_t fake_conforms(io_object_t,const io_name_t);
static kern_return_t fake_children(io_registry_entry_t,const io_name_t,io_iterator_t*);
static IOReturn fake_plugin(io_service_t,CFUUIDRef,CFUUIDRef,IOCFPlugInInterface***,SInt32*);
static IOReturn fake_destroy(IOCFPlugInInterface**);
static kern_return_t fake_name(io_registry_entry_t,io_name_t);
static kern_return_t fake_id(io_registry_entry_t,uint64_t*);
static uid_t fake_uid(void);
#define IORegistryEntryGetRegistryEntryID fake_id
#define IORegistryEntryCreateCFProperty fake_property
#define IOServiceGetMatchingServices fake_services
#define IOServiceMatching fake_matching
#define IOIteratorNext fake_next
#define IOObjectRelease fake_release
#define IOObjectRetain fake_retain
#define IOObjectConformsTo fake_conforms
#define IORegistryEntryGetChildIterator fake_children
#define IORegistryEntryGetName fake_name
#define IOCreatePlugInInterfaceForService fake_plugin
#define IODestroyPlugInInterface fake_destroy
#define geteuid fake_uid
#define kIOMainPortDefault 0
#include "../native.h"
static int mode,opened,closed,attempts,releases,cursor[300],roots;
static unsigned char descriptor_bytes[sizeof(profile)];
static IOUSBDeviceInterface table,*table_ptr=&table;
static IOCFPlugInInterface plugin_table,*plugin_ptr=&plugin_table;
static uid_t fake_uid(void){return mode==15?0:501;}
static kern_return_t fake_id(io_registry_entry_t s,uint64_t*out){*out=s==100?(mode==3?43:42):s;return 0;}
static CFTypeRef fake_property(io_registry_entry_t s,CFStringRef key,CFAllocatorRef a,IOOptionBits o){
 (void)a;(void)o;int64_t n=-1;
 if(s>=100&&s<200){
 if(CFEqual(key,CFSTR("idVendor")))n=0x2ca3;
 if(CFEqual(key,CFSTR("idProduct")))n=0x4006;
 if(CFEqual(key,CFSTR("bcdDevice")))n=mode==5?793:792;
 if(CFEqual(key,CFSTR("locationID")))n=mode==4?999:1234;
 if(CFEqual(key,CFSTR("kUSBCurrentConfiguration")))n=(mode==6||(mode==10&&opened))?2:1;
 }else if(s>=200&&s<206&&CFEqual(key,CFSTR("bInterfaceNumber")))n=s-200;
 return CFNumberCreate(NULL,kCFNumberSInt64Type,&n);
}
static CFMutableDictionaryRef fake_matching(const char*n){assert(!strcmp(n,"IOUSBHostDevice"));return NULL;}
static kern_return_t fake_services(mach_port_t p,CFDictionaryRef d,io_iterator_t*i){(void)p;(void)d;cursor[1]=0;*i=1;return 0;}
static io_object_t fake_next(io_iterator_t i){
 if(i==1)return cursor[i]<roots?100+cursor[i]++:0;
 if(i==2)return cursor[i]<6?200+cursor[i]++:0;
 if((i==202&&mode==8)||i==204||i==205)return cursor[i]++==0?i+100:0;
 return 0;
}
static kern_return_t fake_release(io_object_t s){(void)s;return 0;}
static kern_return_t fake_retain(io_object_t s){(void)s;return 0;}
static boolean_t fake_conforms(io_object_t s,const io_name_t n){assert(!strcmp(n,"IOUSBHostInterface"));return s>=200&&s<206;}
static kern_return_t fake_children(io_registry_entry_t s,const io_name_t plane,io_iterator_t*i){(void)plane;*i=s==100?2:s;cursor[*i]=0;return 0;}
static kern_return_t fake_name(io_registry_entry_t s,io_name_t out){strcpy(out,s==304?(mode==9?"WrongDriver":"AppleUserECM"):"AppleUserECMData");return 0;}
static ULONG fake_com_release(void*s){(void)s;releases++;return 0;}
static HRESULT fake_query(void*s,REFIID id,LPVOID*out){(void)s;(void)id;*out=&table_ptr;return 0;}
static IOReturn fake_plugin(io_service_t s,CFUUIDRef a,CFUUIDRef b,IOCFPlugInInterface***out,SInt32*score){assert(s==100);(void)a;(void)b;*score=1;*out=&plugin_ptr;return 0;}
static IOReturn fake_destroy(IOCFPlugInInterface**p){assert(p==&plugin_ptr);return 0;}
static IOReturn fake_count(void*s,UInt8*n){(void)s;*n=1;return 0;}
static IOReturn fake_descriptor(void*s,UInt8 n,IOUSBConfigurationDescriptorPtr*out){(void)s;assert(n==0);*out=(void*)descriptor_bytes;return 0;}
static IOReturn fake_open(void*s){(void)s;opened++;return mode==11?kIOReturnExclusiveAccess:0;}
static IOReturn fake_reenum(void*s,UInt32 options){(void)s;assert(options==0);assert(opened==1);attempts++;assert(attempts==1);return mode==12?kIOReturnError:0;}
static IOReturn fake_close(void*s){(void)s;closed++;return mode==13?kIOReturnNoDevice:(mode==14?kIOReturnError:0);}
int main(int argc,char**argv){
 assert(argc==2);FILE*f=fopen(argv[1],"rb");assert(f);unsigned char fixture[sizeof(profile)+1];size_t n=fread(fixture,1,sizeof(fixture),f);fclose(f);assert(n==sizeof(profile)&&!memcmp(fixture,profile,n));
 table.GetNumberOfConfigurations=fake_count;table.GetConfigurationDescriptorPtr=fake_descriptor;table.USBDeviceOpen=fake_open;table.USBDeviceClose=fake_close;table.USBDeviceReEnumerate=fake_reenum;table.Release=fake_com_release;plugin_table.QueryInterface=fake_query;
 for(mode=0;mode<=15;mode++){
  opened=closed=attempts=releases=0;roots=mode==1?0:(mode==2?2:1);memcpy(descriptor_bytes,profile,sizeof(profile));if(mode==7)descriptor_bytes[10]^=1;
  int error=0;Context*c=restore_prepare(42,1234,&error);
  if((mode>=1&&mode<=9)||mode==15){assert(error);assert(!opened&&!attempts&&!closed);}
  else {assert(!error);restore_result r=restore_once(c);if(mode==0||mode==13){assert(!r.stage&&r.attempted&&attempts==1&&closed==1);}else{assert(r.stage);if(mode==10)assert(!attempts&&closed==1);if(mode==11)assert(!attempts&&!closed);if(mode==12||mode==14)assert(attempts==1&&closed==1);}}
  restore_release(c);
 }
 puts("PASS 16 native hardware-free cases: unique target, identity, location, revision, configuration, descriptor, interface owners, stale state, busy, API/close errors, no-device close, non-root, success");
 return 0;
}
