package safeusb

import (
	"encoding/hex"
	"errors"
)

// ParseConfiguration is pure offline parsing; malformed evidence never authorizes IO.
func ParseConfiguration(raw []byte, d *Device) error {
	if len(raw) < 9 || raw[0] != 9 || raw[1] != 2 || int(raw[2])|int(raw[3])<<8 != len(raw) {
		return errors.New("invalid configuration header or total length")
	}
	d.Configuration = raw[5]
	d.ConfigurationHex = hex.EncodeToString(raw)
	d.Interfaces = nil
	expected := []int{}
	numbers := map[uint8]bool{}
	for offset := 9; offset < len(raw); {
		if offset+2 > len(raw) {
			return errors.New("truncated descriptor")
		}
		size := int(raw[offset])
		if size < 2 || offset+size > len(raw) {
			return errors.New("invalid descriptor length")
		}
		b := raw[offset : offset+size]
		switch b[1] {
		case 4:
			if size != 9 {
				return errors.New("invalid interface descriptor")
			}
			d.Interfaces = append(d.Interfaces, Interface{Number: b[2], Alternate: b[3], Class: b[5], Subclass: b[6], Protocol: b[7], Endpoints: []Endpoint{}})
			expected = append(expected, int(b[4]))
			numbers[b[2]] = true
		case 5:
			if size < 7 || len(d.Interfaces) == 0 {
				return errors.New("invalid endpoint descriptor")
			}
			last := &d.Interfaces[len(d.Interfaces)-1]
			last.Endpoints = append(last.Endpoints, Endpoint{Address: b[2], Attributes: b[3], MaxPacketSize: uint16(b[4]) | uint16(b[5])<<8, Interval: b[6]})
		case 2:
			return errors.New("nested configuration")
		}
		offset += size
	}
	if len(numbers) != int(raw[4]) {
		return errors.New("interface count mismatch")
	}
	for n, i := range d.Interfaces {
		if len(i.Endpoints) != expected[n] {
			return errors.New("endpoint count mismatch")
		}
	}
	return Validate(*d)
}
