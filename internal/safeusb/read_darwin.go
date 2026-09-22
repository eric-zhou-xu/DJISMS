//go:build darwin && cgo

package safeusb

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <IOKit/IOKitLib.h>
#include <IOKit/IOCFPlugIn.h>
#include <IOKit/usb/IOUSBLib.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

// No libusb: its Darwin enumeration may seize/reconfigure devices.
// Only registry properties and the configuration descriptor pointer getter are
// used. The Apple SDK states the device need not be open for this getter.
typedef struct {
 uint32_t location;
 uint16_t revision;
 uint8_t address;
 uint16_t length;
 unsigned char bytes[65535];
} cached_config;
typedef struct { int count; cached_config items[16]; } cached_result;

static int property(io_service_t service, CFStringRef name, int64_t *out) {
 CFTypeRef value=IORegistryEntryCreateCFProperty(service,name,kCFAllocatorDefault,0);
 if (!value) return 0;
 int ok=CFGetTypeID(value)==CFNumberGetTypeID() && CFNumberGetValue((CFNumberRef)value,kCFNumberSInt64Type,out);
 CFRelease(value); return ok;
}
static int read_cache(cached_result *result) {
 io_iterator_t iterator=0;
 kern_return_t rc=IOServiceGetMatchingServices(kIOMainPortDefault,IOServiceMatching("IOUSBHostDevice"),&iterator);
 if (rc!=KERN_SUCCESS) return 1;
 io_service_t service;
 int failure=0;
 while ((service=IOIteratorNext(iterator))) {
  int64_t vendor=0,product=0,active=0,configs=0,revision=0,location=0,address=0;
  if (!property(service,CFSTR("idVendor"),&vendor) || !property(service,CFSTR("idProduct"),&product) || vendor!=0x2ca3 || product!=0x4006) {IOObjectRelease(service);continue;}
  if (result->count>=16 || !property(service,CFSTR("kUSBCurrentConfiguration"),&active) || active<1 || active>255 ||
      !property(service,CFSTR("bNumConfigurations"),&configs) || configs<1 || configs>255 ||
      !property(service,CFSTR("bcdDevice"),&revision) || !property(service,CFSTR("locationID"),&location) ||
      !property(service,CFSTR("USB Address"),&address)) {failure=2;IOObjectRelease(service);break;}
  IOCFPlugInInterface **plugin=NULL;
  IOUSBDeviceInterface **device=NULL;
  SInt32 score=0;
  IOReturn status=IOCreatePlugInInterfaceForService(service,kIOUSBDeviceUserClientTypeID,kIOCFPlugInInterfaceID,&plugin,&score);
  IOObjectRelease(service);
  if (status!=kIOReturnSuccess || !plugin) {if (plugin) (*plugin)->Release(plugin);failure=3;break;}
  HRESULT query=(*plugin)->QueryInterface(plugin,CFUUIDGetUUIDBytes(kIOUSBDeviceInterfaceID),(LPVOID *)&device);
  (*plugin)->Release(plugin);
  if (query || !device) {if (device) (*device)->Release(device);failure=4;break;}
  int found=0;
  for (int i=0;i<configs;i++) {
   IOUSBConfigurationDescriptorPtr descriptor=NULL;
   // No USBDeviceOpen/Seize, interface claim, request, reset, or set operation.
   status=(*device)->GetConfigurationDescriptorPtr(device,(UInt8)i,&descriptor);
   if (status!=kIOReturnSuccess || !descriptor) {failure=5;break;}
   const unsigned char *b=(const unsigned char *)descriptor;
   if (b[0]<9 || b[1]!=2) {failure=6;break;}
   if (b[5]!=(uint8_t)active) continue;
   uint16_t len=(uint16_t)b[2] | ((uint16_t)b[3]<<8);
   if (len<9) {failure=7;break;}
   cached_config *item=&result->items[result->count];
   item->location=(uint32_t)location; item->revision=(uint16_t)revision; item->address=(uint8_t)address;
   item->length=len;memcpy(item->bytes,b,len);found=1;result->count++;break;
  }
  (*device)->Release(device);
  if (failure || !found) {if (!failure) failure=8;break;}
 }
 IOObjectRelease(iterator);
 return failure;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

type SystemReader struct{}

func (SystemReader) Snapshot() (Snapshot, error) {
	out := Snapshot{Schema: 1, Source: "iokit-cached-descriptors-only", Devices: []Device{}}
	result := (*C.cached_result)(C.calloc(1, C.size_t(C.sizeof_cached_result)))
	if result == nil {
		return out, fmt.Errorf("allocation failed")
	}
	defer C.free(unsafe.Pointer(result))
	if rc := C.read_cache(result); rc != 0 {
		return out, fmt.Errorf("cached descriptors unavailable (stage %d); refusing open/request/reset fallback", int(rc))
	}
	for _, item := range result.items[:int(result.count)] {
		raw := C.GoBytes(unsafe.Pointer(&item.bytes[0]), C.int(item.length))
		d := Device{Vendor: VendorID, Product: ProductID, BCDDevice: uint16(item.revision), Address: uint8(item.address), LocationID: uint32(item.location)}
		if err := ParseConfiguration(raw, &d); err != nil {
			return out, err
		}
		out.Devices = append(out.Devices, d)
	}
	return out, nil
}
