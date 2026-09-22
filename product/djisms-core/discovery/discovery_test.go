package discovery

import (
	"encoding/hex"
	"github.com/iniwex5/vohive/internal/safeusb"
	"testing"
)

func validDevice() Device {
	d := Device{RegistryID: 42, Location: 1234, Network: "test0", ECMOwners: 2, InterfaceMask: 63, Profile: safeusb.Device{Vendor: safeusb.VendorID, Product: safeusb.ProductID, BCDDevice: 792, LocationID: 1234}}
	raw, _ := hex.DecodeString(ProfileHex)
	_ = safeusb.ParseConfiguration(raw, &d.Profile)
	for n := 0; n < 6; n++ {
		f := Interface{RegistryID: uint64(100 + n), Number: n}
		switch n {
		case 2:
			f.Class = 255
			f.Endpoints = 3
		case 4:
			f.Class = 2
			f.Subclass = 6
			f.Endpoints = 1
			f.Owner = "AppleUserECM"
		case 5:
			f.Class = 10
			f.Alternate = 1
			f.Endpoints = 2
			f.Owner = "AppleUserECM"
		}
		d.Interfaces = append(d.Interfaces, f)
	}
	return d
}
func TestProfileAndLifecycle(t *testing.T) {
	d := validDevice()
	if e := Validate(d); e != nil {
		t.Fatal(e)
	}
	if _, e := Single([]Device{d, d}); e == nil {
		t.Fatal("ambiguous accepted")
	}
	other := validDevice()
	other.RegistryID++
	if e := Reconcile(d, other); e == nil {
		t.Fatal("replug identity reused")
	}
	other = validDevice()
	other.Location++
	if e := Validate(other); e == nil {
		t.Fatal("location mismatch")
	}
	other = validDevice()
	other.Interfaces[5].Owner = "other"
	if e := Validate(other); e == nil {
		t.Fatal("ECM stolen")
	}
	other = validDevice()
	other.Profile.BCDDevice++
	if e := Validate(other); e == nil {
		t.Fatal("unknown hardware")
	}
}
