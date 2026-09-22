// Package safeusb contains descriptor inspection and a fail-closed Gate 1 policy.
// The production descriptor reader uses cached IOKit properties, never USB writes.
package safeusb

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const VendorID = 0x2ca3
const ProductID = 0x4006

type Endpoint struct {
	Address       uint8  `json:"address"`
	Attributes    uint8  `json:"attributes"`
	MaxPacketSize uint16 `json:"max_packet_size"`
	Interval      uint8  `json:"interval"`
}
type Interface struct {
	Number    uint8      `json:"number"`
	Alternate uint8      `json:"alternate"`
	Class     uint8      `json:"class"`
	Subclass  uint8      `json:"subclass"`
	Protocol  uint8      `json:"protocol"`
	Endpoints []Endpoint `json:"endpoints"`
}
type Device struct {
	LocationID       uint32      `json:"location_id"`
	ConfigurationHex string      `json:"configuration_descriptor_hex"`
	Vendor           uint16      `json:"vendor_id"`
	Product          uint16      `json:"product_id"`
	BCDDevice        uint16      `json:"bcd_device"`
	Address          uint8       `json:"address"`
	Configuration    uint8       `json:"configuration"`
	Interfaces       []Interface `json:"interfaces"`
}
type Snapshot struct {
	Schema        int      `json:"schema"`
	Source        string   `json:"source"`
	Gate1Approved bool     `json:"gate1_approved"`
	Devices       []Device `json:"devices"`
}

// ReadFixture never loads a USB reader. Input is descriptive evidence, not approval.
func ReadFixture(r io.Reader) (Snapshot, error) {
	var s Snapshot
	raw, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	if err != nil {
		return s, err
	}
	if len(raw) > 1<<20 {
		return s, errors.New("fixture exceeds 1 MiB")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&s); err != nil {
		return s, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return s, errors.New("fixture must contain exactly one JSON document")
	}
	if s.Schema != 1 || s.Gate1Approved {
		return s, errors.New("invalid schema or fixture attempts to grant approval")
	}
	if len(s.Devices) > 16 {
		return s, errors.New("too many devices")
	}
	for _, dev := range s.Devices {
		if err := Validate(dev); err != nil {
			return s, err
		}
	}
	s.Source = "offline-fixture"
	return s, nil
}

func Validate(d Device) error {
	if d.Vendor != VendorID || d.Product != ProductID {
		return errors.New("unexpected USB identity")
	}
	if d.Configuration == 0 || len(d.Interfaces) == 0 || len(d.Interfaces) > 64 {
		return errors.New("invalid active configuration")
	}
	seen := map[[2]uint8]bool{}
	for _, i := range d.Interfaces {
		key := [2]uint8{i.Number, i.Alternate}
		if seen[key] {
			return errors.New("duplicate interface/alternate")
		}
		seen[key] = true
		if len(i.Endpoints) > 30 {
			return errors.New("too many endpoints")
		}
		eps := map[uint8]bool{}
		for _, ep := range i.Endpoints {
			if ep.Address&0x0f == 0 || ep.Address&0x70 != 0 || ep.MaxPacketSize == 0 || eps[ep.Address] {
				return fmt.Errorf("invalid or duplicate endpoint on interface %d", i.Number)
			}
			eps[ep.Address] = true
		}
	}
	return nil
}

// DescriptorReader is deliberately unable to open, claim, reset or write a device.
type DescriptorReader interface{ Snapshot() (Snapshot, error) }
