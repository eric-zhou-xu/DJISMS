package purge

import (
	"context"
	"encoding/hex"

	"errors"
	"fmt"
	"github.com/iniwex5/vohive/internal/safeusb"
	"github.com/iniwex5/vohive/internal/smsreceive"

	"strings"
	"testing"
	"time"
)

type fake struct {
	commands            []Command
	pending             []byte
	target              string
	targetIndex         int
	hashes              map[int]string
	targetLength        int
	closed              int
	failWrite, failRead uint8
	override            map[uint8]string
	chunk               int
}

func (f *fake) snapshot() (safeusb.Snapshot, error) {
	b, _ := hex.DecodeString(reviewedHex)
	d := safeusb.Device{Vendor: safeusb.VendorID, Product: safeusb.ProductID, BCDDevice: 792, LocationID: uint32(0)}
	e := safeusb.ParseConfiguration(b, &d)
	return safeusb.Snapshot{Schema: 1, Devices: []safeusb.Device{d}}, e
}
func (f *fake) open() error  { return nil }
func (f *fake) close() error { f.closed++; return nil }
func (f *fake) write(c Command, _ time.Duration) error {
	f.commands = append(f.commands, c)
	if c.Kind == f.failWrite {
		return errors.New("write timeout")
	}
	wire, _ := c.wire()
	body := ""
	switch c.Kind {
	case 1, 13:
		body = "+CMGF: 0"
	case 2, 6, 8:
		body = fmt.Sprintf(`+CPMS: "ME",%d,23,"ME",%d,23,"ME",%d,23`, len(f.hashes), len(f.hashes), len(f.hashes))
	case 10, 12:
		body = fmt.Sprintf(`+CPMS: "ME",%d,23,"ME",%d,23,"ME",%d,23`, len(f.hashes)-1, len(f.hashes)-1, len(f.hashes)-1)
	case 3, 14:
		body = "+CNMI: 2,1,0,0,0"
	case 4, 15:
		body = "+CSMS: 0,1,1,1"
	case 5, 11:
		for i := 0; i < 23; i++ {
			if _, ok := f.hashes[i]; !ok {
				continue
			}
			if c.Kind == 11 && i == f.targetIndex {
				continue
			}
			pdu, n := "0000", 1
			if i == f.targetIndex {
				pdu, n = f.target, f.targetLength
			}
			body += fmt.Sprintf("+CMGL: %d,1,,%d\r\n%s\r\n", i, n, pdu)
		}
	case 7:
		body = fmt.Sprintf("+CMGR: 1,,%d\r\n%s", f.targetLength, f.target)
	}
	if b, ok := f.override[c.Kind]; ok {
		body = b
	}
	f.pending = []byte(wire + "\r\r\n" + body + "\r\nOK\r\n")
	return nil
}
func (f *fake) readDiagnostic(b []byte, _ time.Duration) (ReadDiagnostic, error) {
	if len(f.commands) > 0 && f.commands[len(f.commands)-1].Kind == f.failRead {
		return classifyRead(ioTimeout, 512, 512), errTimeout
	}
	if len(f.pending) == 0 {
		return classifyRead(ioTimeout, 512, 512), errTimeout
	}
	if f.chunk > 0 && len(b) > f.chunk {
		b = b[:f.chunk]
	}
	n := copy(b, f.pending)
	f.pending = f.pending[n:]
	return classifyRead(0, uint32(n), 512), nil
}

type sink struct {
	fail     string
	reject   bool
	approved bool
	events   []string
}

func (s *sink) Save(k string, _ any) error {
	s.events = append(s.events, k)
	if s.fail == k {
		return errors.New("disk full")
	}
	return nil
}
func (s *sink) Authorize(_ context.Context, p Predelete) error {
	if p.Index < 0 || p.Index > 22 || p.Storage != "ME" {
		return errors.New("invalid request")
	}
	if s.reject {
		return errors.New("archive failed")
	}
	s.approved = true
	return nil
}
func setup(t *testing.T) (*fake, Config) {
	t.Helper()
	target := "0011"
	h, _ := hashMessage(smsreceive.RawMessage{PDU: target, TPDULength: 1})
	generic, _ := hashMessage(smsreceive.RawMessage{PDU: "0000", TPDULength: 1})
	c := Config{ExpectedHashes: map[int]string{}, TargetIndex: 1, TargetHash: h}
	for i := 0; i < 23; i++ {
		c.ExpectedHashes[i] = generic
	}
	c.ExpectedHashes[1] = h
	return &fake{target: target, targetLength: 1, targetIndex: 1, hashes: c.ExpectedHashes, override: map[uint8]string{}}, c
}
func run(t *testing.T, f *fake, c Config, s *sink) (Summary, error) {
	t.Helper()
	tr := &Transport{b: f}
	if e := tr.Connect(context.Background()); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return tr.Purge(ctx, c, s)
}
func deleted(f *fake) int {
	n := 0
	for _, c := range f.commands {
		if c.Kind == 9 {
			n++
		}
	}
	return n
}
func TestApprovedSingleDeleteAndPostInventory(t *testing.T) {
	f, c := setup(t)
	s := &sink{}
	r, e := run(t, f, c, s)
	if e != nil || !r.DeleteConfirmed || !r.Closed || r.FinalUsed != 22 || deleted(f) != 1 || len(f.commands) != 15 || !s.approved {
		t.Fatalf("gate: %+v %v", r, e)
	}
}
func TestAllReadAndWriteFailuresStopNoRetry(t *testing.T) {
	for kind := uint8(1); kind <= 15; kind++ {
		for _, read := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/read=%v", kind, read), func(t *testing.T) {
				f, c := setup(t)
				if read {
					f.failRead = kind
				} else {
					f.failWrite = kind
				}
				r, e := run(t, f, c, &sink{})
				if e == nil || !r.Closed || len(f.commands) != int(kind) || deleted(f) > 1 {
					t.Fatalf("did not stop at %d: %v %+v", kind, e, r)
				}
			})
		}
	}
}
func TestPreconditionsAndArchiveFailuresNeverDelete(t *testing.T) {
	for _, tc := range []string{"hash", "storage", "inventory", "target", "archive", "rawsave", "intent"} {
		t.Run(tc, func(t *testing.T) {
			f, c := setup(t)
			s := &sink{}
			switch tc {
			case "hash":
				c.ExpectedHashes[1] = strings.Repeat("0", 64)
			case "storage":
				f.override[2] = `+CPMS: "SM",23,23,"SM",23,23,"SM",23,23`
			case "inventory":
				f.override[5] = ""
			case "target":
				f.override[7] = "+CMGR: 1,,1\r\n0000"
			case "archive":
				s.reject = true
			case "rawsave":
				s.fail = "usb_read"
			case "intent":
				s.fail = "delete_attempt"
			}
			_, e := run(t, f, c, s)
			if e == nil || deleted(f) != 0 || f.closed != 1 {
				t.Fatal("unsafe delete", e)
			}
		})
	}
}
func TestPostMismatchStopsAfterExactlyOneDelete(t *testing.T) {
	for _, kind := range []uint8{10, 11, 12, 13, 14, 15} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			f, c := setup(t)
			f.override[kind] = "+CPMS: \"ME\",23,23,\"ME\",23,23,\"ME\",23,23"
			r, e := run(t, f, c, &sink{})
			if e == nil || r.DeleteConfirmed || deleted(f) != 1 || f.closed != 1 {
				t.Fatalf("%+v %v", r, e)
			}
		})
	}
}
func TestUnexpectedNewSMSOrMalformedReplyStops(t *testing.T) {
	for _, body := range []string{`+CMTI: "ME",22`, "RING", "+CMGR: 1,,1\r\nERROR", "+CMT: ,1\r\n0000"} {
		f, c := setup(t)
		f.override[7] = body
		_, e := run(t, f, c, &sink{})
		if e == nil || deleted(f) != 0 {
			t.Fatal("did not fail closed")
		}
	}
}
func TestCommandAPIBoundedSlotsNoBulk(t *testing.T) {
	for k := 0; k < 256; k++ {
		for _, i := range []int{-1, 0, 1, 22, 23, 65535} {
			wire, e := (Command{Kind: uint8(k), Index: i}).wire()
			valid := k >= 1 && k <= 15 && ((k == 7 || k == 9) && (i >= 0 && i <= 22) || (k != 7 && k != 9) && i == 0)
			if (e == nil) != valid {
				t.Fatalf("%d %d %s %v", k, i, wire, e)
			}
			if strings.Contains(wire, ",") {
				t.Fatal("bulk flag")
			}
		}
	}
}
func TestEveryRemainingSlotAndCapacityToEmpty(t *testing.T) {
	f, c := setup(t)
	delete(c.ExpectedHashes, 1)
	for idx := 0; idx < 23; idx++ {
		if idx == 1 {
			continue
		}
		f.commands = nil
		f.closed = 0
		f.targetIndex = idx
		c.TargetIndex = idx
		c.TargetHash = c.ExpectedHashes[idx]
		f.target = "0000"
		r, e := run(t, f, c, &sink{})
		if e != nil || !r.DeleteConfirmed || r.FinalUsed != len(c.ExpectedHashes)-1 || deleted(f) != 1 {
			t.Fatalf("slot%d %+v %v", idx, r, e)
		}
		delete(c.ExpectedHashes, idx)
	}
}
func FuzzInventoryNeverAcceptsCorruption(f *testing.F) {
	f.Add("0000", 1, 0)
	f.Add("00", 1, 0)
	f.Fuzz(func(t *testing.T, pdu string, length, index int) {
		if len(pdu) > 2048 {
			return
		}
		c := Config{TargetIndex: 0, TargetHash: strings.Repeat("0", 64), ExpectedHashes: map[int]string{0: strings.Repeat("0", 64)}}
		e := reconcileInventory([]smsreceive.RawMessage{{Index: index, PDU: pdu, TPDULength: length}}, c, false)
		if e == nil {
			t.Fatal("unexpected matching zero hash")
		}
	})
}
func TestHashIncludesOriginalPDUBytes(t *testing.T) {
	h, e := hashMessage(smsreceive.RawMessage{PDU: "0000", TPDULength: 2})
	if e == nil || h != "" {
		t.Fatal("bad declared length accepted")
	}
}
