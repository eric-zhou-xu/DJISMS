//go:build darwin && cgo

package usbrestore

/*
#cgo LDFLAGS: -framework SystemConfiguration -framework CoreFoundation
#include <SystemConfiguration/SystemConfiguration.h>
#include <stdlib.h>
static int media_inactive(const char*name){
 CFStringRef nic=CFStringCreateWithCString(NULL,name,kCFStringEncodingUTF8);if(!nic)return -1;
 CFStringRef key=SCDynamicStoreKeyCreateNetworkInterfaceEntity(NULL,kSCDynamicStoreDomainState,nic,kSCEntNetLink);CFRelease(nic);if(!key)return -1;
 SCDynamicStoreRef store=SCDynamicStoreCreate(NULL,CFSTR("DJISMS link observation"),NULL,NULL);if(!store){CFRelease(key);return -1;}
 CFPropertyListRef value=SCDynamicStoreCopyValue(store,key);CFRelease(store);CFRelease(key);
 if(!value)return 0; // No explicit inactive observation, so no recovery permission.
 int result=0;
 if(CFGetTypeID(value)==CFDictionaryGetTypeID()){
  CFTypeRef active=CFDictionaryGetValue(value,kSCPropNetLinkActive);
  if(active&&CFGetTypeID(active)==CFBooleanGetTypeID())result=CFBooleanGetValue(active)?0:1;
 }
 CFRelease(value);return result;
}
*/
import "C"
import (
	"errors"
	"github.com/iniwex5/vohive/product/djisms-core/discovery"
	"unsafe"
)

func MediaInactive(d discovery.Device) (bool, error) {
	if e := discovery.Validate(d); e != nil {
		return false, e
	}
	nic := C.CString(d.Network)
	defer C.free(unsafe.Pointer(nic))
	r := C.media_inactive(nic)
	if r < 0 {
		return false, errors.New("cannot observe ECM media state")
	}
	return r == 1, nil
}
