//go:build darwin && cgo && djisms_native

package purge

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include "native.h"
#include <mach/mach_error.h>
*/
import "C"

import (
	"encoding/hex"
	"fmt"
	"github.com/iniwex5/vohive/internal/safeusb"
	"time"
	"unsafe"
)

// NewNative constructs an unopened transport. The separately tagged one-shot
// command uses it only after explicit invocation and durable receipt reservation.
func NewNative(registryID uint64, location uint32) *Transport {
	return &Transport{b: &nativeBackend{registryID: registryID}, location: location}
}

type nativeBackend struct {
	port       *C.g1_port
	registryID uint64
}

func (*nativeBackend) snapshot() (safeusb.Snapshot, error) {
	return (safeusb.SystemReader{}).Snapshot()
}
func (b *nativeBackend) open() error {
	raw, err := hex.DecodeString(reviewedHex)
	if err != nil {
		return err
	}
	if rc := C.g1_open((*C.uchar)(unsafe.Pointer(&raw[0])), C.uint(len(raw)), C.uint64_t(b.registryID), &b.port); rc != 0 {
		return fmt.Errorf("interface 2 acquisition rejected: 0x%08x", uint32(rc))
	}
	return nil
}
func timeoutMS(d time.Duration) C.uint {
	n := d / time.Millisecond
	if n < 1 {
		n = 1
	}
	if n > 250 {
		n = 250
	}
	return C.uint(n)
}
func (b *nativeBackend) write(q Command, timeout time.Duration) error {
	if _, err := q.wire(); err != nil {
		return err
	}
	if rc := C.g1_query(b.port, C.uint(q.Kind), C.uint(q.Index), timeoutMS(timeout)); rc != 0 {
		return fmt.Errorf("query write failed (not retried): 0x%08x", uint32(rc))
	}
	return nil
}
func (b *nativeBackend) readDiagnostic(buf []byte, timeout time.Duration) (ReadDiagnostic, error) {
	if len(buf) == 0 || b.port == nil {
		return ReadDiagnostic{}, fmt.Errorf("empty buffer or unopened port")
	}
	ms := timeoutMS(timeout)
	n := C.uint(len(buf))
	pipe := uint8(b.port.in_pipe)
	started := time.Now()
	rc := C.g1_read(b.port, unsafe.Pointer(&buf[0]), &n, ms)
	d := classifyRead(uint32(rc), uint32(n), uint32(len(buf)))
	d.PipeRef = pipe
	d.TimeoutMS = uint32(ms)
	d.ElapsedUS = time.Since(started).Microseconds()
	d.OSMessage = C.GoString(C.mach_error_string(C.mach_error_t(rc)))
	var err error
	if uint32(rc) == ioTimeout || uint32(rc) == usbTransactionTimeout {
		err = errTimeout
	} else if rc != 0 {
		err = fmt.Errorf("IOKit read error: %s", d.ReturnHex)
	}
	if d.Category == "invalid_size" {
		err = fmt.Errorf("IOKit returned an invalid read size")
	}
	return d, err
}
func (b *nativeBackend) close() error {
	if b.port == nil {
		return nil
	}
	rc := C.g1_close(b.port)
	b.port = nil
	if rc != 0 {
		return fmt.Errorf("USBInterfaceClose failed: 0x%08x", uint32(rc))
	}
	return nil
}
