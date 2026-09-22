//go:build darwin && cgo && djisms_native

package usbrestore

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include "native.h"
*/
import "C"
import (
	"errors"
	"fmt"
	"github.com/iniwex5/vohive/product/djisms-core/discovery"
)

// ReEnumerate checks the current device without opening it, calls persist, then
// makes at most one non-seizing device-open / reenumerate(0) attempt.
func ReEnumerate(d discovery.Device, persist func() error) (Result, error) {
	var out Result
	if persist == nil {
		return out, errors.New("durable intent callback required")
	}
	if e := validate(d); e != nil {
		return out, e
	}
	var stage C.int
	h := C.restore_prepare(C.uint64_t(d.RegistryID), C.uint32_t(d.Location), &stage)
	if h != nil {
		defer C.restore_release(h)
	}
	if stage != 0 || h == nil {
		return out, fmt.Errorf("USB reenumeration preflight rejected: %d", int(stage))
	}
	if e := persist(); e != nil {
		return out, e
	}
	r := C.restore_once(h)
	out = Result{Stage: int(r.stage), OpenCode: uint32(r.open_rc), ReenumerateCode: uint32(r.reenumerate_rc), CloseCode: uint32(r.close_rc), Attempted: r.attempted != 0}
	if out.Stage != 0 {
		return out, fmt.Errorf("USB reenumeration stopped: stage=%d open=0x%08x reenumerate=0x%08x close=0x%08x", out.Stage, out.OpenCode, out.ReenumerateCode, out.CloseCode)
	}
	return out, nil
}
