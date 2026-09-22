//go:build darwin && cgo

// Package discovery reads IOKit registry identity without opening USB interfaces.
package discovery

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <IOKit/IOKitLib.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdint.h>
#include <string.h>

typedef struct { uint64_t id; int number,alternate,cl,sub,protocol,endpoints; char owner[256]; } dj_interface;
typedef struct { dj_interface iface[6]; int iface_count; uint64_t id; uint32_t location; int ecm; char network[128]; int interfaces; } dj_device;
typedef struct { int count; dj_device items[16]; } dj_list;
static int dj_number(io_service_t s,CFStringRef key,int64_t *out) {
 CFTypeRef v=IORegistryEntryCreateCFProperty(s,key,kCFAllocatorDefault,0);
 if(!v)return 0;
 int ok=CFGetTypeID(v)==CFNumberGetTypeID() && CFNumberGetValue(v,kCFNumberSInt64Type,out);
 CFRelease(v);return ok;
}
static int dj_scan(dj_list *out) {
 io_iterator_t it=0; if(IOServiceGetMatchingServices(kIOMainPortDefault,IOServiceMatching("IOUSBHostDevice"),&it))return 1;
 io_service_t s; int error=0;
 while((s=IOIteratorNext(it))) {
  int64_t vid=0,pid=0,loc=0;
  if(!dj_number(s,CFSTR("idVendor"),&vid)||!dj_number(s,CFSTR("idProduct"),&pid)||vid!=0x2ca3||pid!=0x4006){IOObjectRelease(s);continue;}
  if(out->count>=16){error=2;IOObjectRelease(s);break;}
  dj_device *d=&out->items[out->count++];
  if(IORegistryEntryGetRegistryEntryID(s,&d->id)||!d->id||!dj_number(s,CFSTR("locationID"),&loc)){error=3;IOObjectRelease(s);break;}
  d->location=(uint32_t)loc;
  io_iterator_t children=0;
  if(IORegistryEntryCreateIterator(s,kIOServicePlane,kIORegistryIterateRecursively,&children)){error=4;IOObjectRelease(s);break;}
  io_service_t child;
  while((child=IOIteratorNext(children))) {
   if(IOObjectConformsTo(child,"IOUSBHostInterface")) {
    int64_t n=-1,v=-1; if(dj_number(child,CFSTR("bInterfaceNumber"),&n)&&n>=0&&n<=5){
     if((d->interfaces&(1<<(int)n)) || d->iface_count>=6){error=6;IOObjectRelease(child);break;}
     d->interfaces|=1<<(int)n;d->iface_count++;
     dj_interface *f=&d->iface[n];f->number=(int)n;
     if(IORegistryEntryGetRegistryEntryID(child,&f->id))error=7;
     if(!dj_number(child,CFSTR("bAlternateSetting"),&v))error=7;f->alternate=(int)v;
     if(!dj_number(child,CFSTR("bInterfaceClass"),&v))error=7;f->cl=(int)v;
     if(!dj_number(child,CFSTR("bInterfaceSubClass"),&v))error=7;f->sub=(int)v;
     if(!dj_number(child,CFSTR("bInterfaceProtocol"),&v))error=7;f->protocol=(int)v;
     if(!dj_number(child,CFSTR("bNumEndpoints"),&v))error=7;f->endpoints=(int)v;
     CFTypeRef owner=IORegistryEntryCreateCFProperty(child,CFSTR("UsbExclusiveOwner"),kCFAllocatorDefault,0);
     if(owner){if(CFGetTypeID(owner)!=CFStringGetTypeID()||!CFStringGetCString(owner,f->owner,sizeof(f->owner),kCFStringEncodingUTF8))error=8;CFRelease(owner);}
     if((n==4||n==5)&&!strcmp(f->owner,"AppleUserECM"))d->ecm++;
    }
   }

   if(IOObjectConformsTo(child,"IOEthernetInterface")) {
    CFTypeRef name=IORegistryEntryCreateCFProperty(child,CFSTR("BSD Name"),kCFAllocatorDefault,0);
    if(name) {
     if(CFGetTypeID(name)==CFStringGetTypeID()) {
      if(d->network[0] || !CFStringGetCString(name,d->network,sizeof(d->network),kCFStringEncodingUTF8))error=5;
     }
     CFRelease(name);
    }
   }
   IOObjectRelease(child);
  }
  IOObjectRelease(children); IOObjectRelease(s);
  if(error)break;
 }
 IOObjectRelease(it);return error;
}
*/
import "C"

import (
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/iniwex5/vohive/internal/safeusb"
	"reflect"
	"unsafe"
)

const ProfileHex = "09020201060100a0fa0904000002ffffff0007058102000200070501020002000904010003ff00000005240010010524010000042402020524060000070583030a000907058202000200070502020002000904020003ff00000005240010010524010000042402020524060000070585030a000907058402000200070503020002000904030003ff00000005240010010524010000042402020524060000070587030a00090705860200020007050402000200080b040202060009090404000102060006052400100105240604050d240f0700000000ea050000000705890310000909040500000a00000009040501020a0000080705880200020007050502000200"

type Interface struct {
	RegistryID uint64 `json:"registry_id"`
	Number     int    `json:"number"`
	Alternate  int    `json:"alternate"`
	Class      int    `json:"class"`
	Subclass   int    `json:"subclass"`
	Protocol   int    `json:"protocol"`
	Endpoints  int    `json:"endpoints"`
	Owner      string `json:"owner"`
}
type Device struct {
	Interfaces    []Interface    `json:"interfaces"`
	RegistryID    uint64         `json:"registry_id"`
	Location      uint32         `json:"location"`
	Network       string         `json:"network_interface"`
	ECMOwners     int            `json:"ecm_owners"`
	InterfaceMask int            `json:"interface_mask"`
	Profile       safeusb.Device `json:"profile"`
}

func Validate(d Device) error {
	if d.RegistryID == 0 || d.Network == "" || d.ECMOwners != 2 || d.InterfaceMask != 63 {
		return errors.New("device identity or Apple ECM ownership incomplete")
	}
	if len(d.Interfaces) != 6 {
		return errors.New("missing live interfaces")
	}
	for _, f := range d.Interfaces {
		if f.RegistryID == 0 || f.Number < 0 || f.Number > 5 {
			return errors.New("invalid interface identity")
		}
		switch f.Number {
		case 2:
			if f.Alternate != 0 || f.Class != 255 || f.Subclass != 0 || f.Protocol != 0 || f.Endpoints != 3 {
				return errors.New("AT interface changed")
			}
		case 4:
			if f.Alternate != 0 || f.Class != 2 || f.Subclass != 6 || f.Endpoints != 1 || f.Owner != "AppleUserECM" {
				return errors.New("ECM control changed")
			}
		case 5:
			if f.Alternate != 1 || f.Class != 10 || f.Subclass != 0 || f.Endpoints != 2 || f.Owner != "AppleUserECM" {
				return errors.New("ECM data changed")
			}
		}
	}
	raw, _ := hex.DecodeString(ProfileHex)
	expected := safeusb.Device{Vendor: safeusb.VendorID, Product: safeusb.ProductID, BCDDevice: 792, LocationID: d.Location}
	if e := safeusb.ParseConfiguration(raw, &expected); e != nil {
		return e
	}
	actual := d.Profile
	actual.Address = 0
	if !reflect.DeepEqual(actual, expected) {
		return errors.New("unsupported hardware revision or USB composition")
	}
	return nil
}
func Snapshot() ([]Device, error) {
	var before C.dj_list
	if rc := C.dj_scan(&before); rc != 0 {
		return nil, fmt.Errorf("registry discovery failed: %d", int(rc))
	}
	profiles, e := (safeusb.SystemReader{}).Snapshot()
	if e != nil {
		return nil, e
	}
	var after C.dj_list
	if rc := C.dj_scan(&after); rc != 0 {
		return nil, fmt.Errorf("registry verification failed: %d", int(rc))
	}
	if before.count != after.count {
		return nil, errors.New("device set changed during discovery")
	}
	devices := []Device{}
	for _, a := range before.items[:int(before.count)] {
		found := false
		for _, b := range after.items[:int(after.count)] {
			if C.memcmp(unsafe.Pointer(&a.iface[0]), unsafe.Pointer(&b.iface[0]), C.size_t(C.sizeof_dj_interface*6)) == 0 && a.id == b.id && a.location == b.location && a.ecm == b.ecm && a.interfaces == b.interfaces && C.GoString(&a.network[0]) == C.GoString(&b.network[0]) {
				found = true
			}
		}
		if !found {
			return nil, errors.New("device identity changed during discovery")
		}
		d := Device{RegistryID: uint64(a.id), Location: uint32(a.location), Network: C.GoString(&a.network[0]), ECMOwners: int(a.ecm), InterfaceMask: int(a.interfaces)}
		for _, f := range a.iface {
			d.Interfaces = append(d.Interfaces, Interface{uint64(f.id), int(f.number), int(f.alternate), int(f.cl), int(f.sub), int(f.protocol), int(f.endpoints), C.GoString(&f.owner[0])})
		}
		matches := 0
		for _, p := range profiles.Devices {
			if p.LocationID == d.Location {
				d.Profile = p
				matches++
			}
		}
		if matches != 1 {
			return nil, errors.New("ambiguous descriptor identity")
		}
		devices = append(devices, d)
	}
	if len(profiles.Devices) != len(devices) {
		return nil, errors.New("descriptor and registry inventories differ")
	}
	return devices, nil
}
func Single(devices []Device) (Device, error) {
	if len(devices) != 1 {
		return Device{}, errors.New("connect exactly one supported DJI device")
	}
	d := devices[0]
	return d, Validate(d)
}
func Reconcile(before, after Device) error {
	if before.RegistryID != after.RegistryID || !reflect.DeepEqual(before, after) {
		return errors.New("USB/ECM identity or composition changed")
	}
	return Validate(after)
}
