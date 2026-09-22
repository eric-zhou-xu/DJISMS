//go:build darwin && cgo && smsreceive_native

package smsreceive

import "testing"

// This is the only test that uses a native type. Construction and Close before
// Connect must not enumerate, acquire, read or write hardware.
func TestNativeConstructorIsInert(t *testing.T) {
	tr := NewNative()
	b, ok := tr.b.(*nativeBackend)
	if !ok || b.port != nil || tr.connected {
		t.Fatal("constructor opened a native handle")
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
}
