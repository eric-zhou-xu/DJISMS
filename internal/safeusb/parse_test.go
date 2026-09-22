package safeusb

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"os"
	"reflect"
	"testing"
)

func configBytes() []byte {
	return []byte{9, 2, 32, 0, 1, 1, 0, 0x80, 50, 9, 4, 2, 0, 2, 255, 0, 0, 0, 7, 5, 0x83, 2, 0, 2, 0, 7, 5, 3, 2, 0, 2, 0}
}
func TestCachedEndpointMap(t *testing.T) {
	d := Device{Vendor: VendorID, Product: ProductID}
	raw := configBytes()
	if err := ParseConfiguration(raw, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Interfaces) != 1 || d.Interfaces[0].Endpoints[0].Address != 0x83 || d.Interfaces[0].Endpoints[1].Address != 3 || d.ConfigurationHex != hex.EncodeToString(raw) {
		t.Fatal(d)
	}
}
func TestMalformedDescriptorsFailClosed(t *testing.T) {
	for _, mutate := range []func([]byte) []byte{
		func(b []byte) []byte { return b[:8] }, func(b []byte) []byte { b[2]++; return b }, func(b []byte) []byte { b[9] = 0; return b }, func(b []byte) []byte { b[9] = 255; return b }, func(b []byte) []byte { b[13] = 3; return b }, func(b []byte) []byte { b[4] = 2; return b }, func(b []byte) []byte { b[27] = 0x83; return b },
	} {
		d := Device{Vendor: VendorID, Product: ProductID}
		if ParseConfiguration(mutate(configBytes()), &d) == nil {
			t.Fatal("malformed descriptor accepted")
		}
	}
}
func FuzzConfigurationParser(f *testing.F) {
	f.Add(configBytes())
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		d := Device{Vendor: VendorID, Product: ProductID}
		_ = ParseConfiguration(b, &d)
	})
}

func TestRecordedCacheFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/baiwang-ecm.json")
	if err != nil {
		t.Fatal(err)
	}
	s, err := ReadFixture(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Devices) != 1 || s.Gate1Approved {
		t.Fatal("unexpected fixture")
	}
	original := s.Devices[0]
	b, err := hex.DecodeString(original.ConfigurationHex)
	if err != nil {
		t.Fatal(err)
	}
	parsed := Device{Vendor: VendorID, Product: ProductID}
	if err := ParseConfiguration(b, &parsed); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parsed.Interfaces, original.Interfaces) {
		t.Fatal("raw and structured descriptors diverged")
	}
	for _, iface := range []uint8{2, 3} {
		p := profile{layout: original, iface: iface, in: 0x84, out: 3}
		if iface == 3 {
			p.in = 0x86
			p.out = 4
		}
		if !matches(p, original) {
			t.Fatal("recorded interface layout does not match", iface)
		}
	}
	f := &fakeTransport{}
	locked := NewGate1Session(f)
	if err := locked.Connect(context.Background(), original); !errors.Is(err, ErrLocked) {
		t.Fatal(err)
	}
	if len(f.calls) != 0 {
		t.Fatal("fixture authorized hardware", f.calls)
	}
}
