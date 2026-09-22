package receive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/iniwex5/vohive/internal/safeusb"
	"github.com/iniwex5/vohive/internal/smsreceive"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

type fake struct {
	pending               []byte
	commands              []Command
	opens, closes         int
	notify                string
	disconnect, writeFail bool
	onWrite               func(Command)
	override              map[uint8]string
	faultAfter            int
	faultCode             uint32
	closeErr              error
	faultIdle             bool
}

func (f *fake) snapshot() (safeusb.Snapshot, error) {
	raw, _ := hex.DecodeString(reviewedHex)
	d := safeusb.Device{Vendor: safeusb.VendorID, Product: safeusb.ProductID, BCDDevice: 792, LocationID: uint32(0)}
	e := safeusb.ParseConfiguration(raw, &d)
	return safeusb.Snapshot{Schema: 1, Devices: []safeusb.Device{d}}, e
}
func (f *fake) open() error  { f.opens++; return nil }
func (f *fake) close() error { f.closes++; return f.closeErr }
func (f *fake) write(c Command, d time.Duration) error {
	if d <= 0 || d > 250*time.Millisecond {
		panic("write bound")
	}
	f.commands = append(f.commands, c)
	if f.onWrite != nil {
		f.onWrite(c)
	}
	if f.writeFail {
		return errors.New("write error")
	}
	wire, _ := c.wire()
	body := ""
	switch c.Kind {
	case 1, 9:
		body = "+CMGF: 0"
	case 2, 6:
		body = `+CPMS: "ME",11,23,"ME",11,23,"ME",11,23`
	case 3, 10:
		body = "+CNMI: 2,1,0,0,0"
	case 4, 11:
		body = "+CSMS: 0,1,1,1"
	case 5:
		for i := 0; i < 11; i++ {
			body += fmt.Sprintf("+CMGL: %d,0,,1\r\n0000\r\n", i)
		}
		body = strings.TrimSuffix(body, "\r\n")
	case 7:
		body = "+CMGR: 0,,1\r\n0000"
	case 8:
		body = `+CPMS: "ME",12,23,"ME",12,23,"ME",12,23`
	}
	if v, ok := f.override[c.Kind]; ok {
		body = v
	}
	f.pending = []byte(wire + "\r\r\n" + body + "\r\nOK\r\n")
	return nil
}
func (f *fake) readDiagnostic(b []byte, d time.Duration) (ReadDiagnostic, error) {
	if d <= 0 || d > 200*time.Millisecond {
		panic("read bound")
	}
	if f.faultCode != 0 && len(f.commands) >= f.faultAfter && (!f.faultIdle || (len(f.pending) == 0 && f.notify == "")) {
		return classifyRead(f.faultCode, 512, 512), errors.New("injected IOKit interruption")
	}
	if len(f.pending) > 0 {
		n := copy(b, f.pending)
		f.pending = f.pending[n:]
		return classifyRead(0, uint32(n), uint32(len(b))), nil
	}
	if f.disconnect && len(f.commands) >= 6 {
		return classifyRead(0xe00002c0, 512, 512), errors.New("removed")
	}
	if len(f.commands) >= 6 && f.notify != "" {
		n := copy(b, f.notify)
		f.notify = f.notify[n:]
		return classifyRead(0, uint32(n), uint32(len(b))), nil
	}
	return classifyRead(usbTransactionTimeout, 512, 512), errTimeout
}

type sink struct {
	events                       []string
	ready                        bool
	received, directs, snapshots int
	cancel                       context.CancelFunc
	fail                         string
}

func (s *sink) Save(k string, v any) error {
	s.events = append(s.events, k)
	if k == s.fail {
		return errors.New("disk full")
	}
	return nil
}
func (s *sink) Ready(n int) error { s.ready = true; return nil }
func (s *sink) Pulse(n int) error { return nil }
func (s *sink) Message(n Notice, m smsreceive.RawMessage) error {
	if n.Index != m.Index || m.PDU != "0000" {
		panic("wrong message")
	}
	s.received++
	s.cancel()
	return nil
}
func (s *sink) Direct(d Direct) error {
	if d.StorageIndex != nil || d.ReportedStatus != nil {
		panic("invented metadata")
	}
	s.directs++
	return nil
}
func (s *sink) Snapshot(ms []smsreceive.RawMessage) error { s.snapshots++; return nil }
func cfg() Config {
	m := map[int]bool{}
	hashes := map[int]string{}
	for i := 0; i < 10; i++ {
		m[i] = true
		hashes[i] = fmt.Sprintf("%x", sha256.Sum256([]byte("0000")))
	}
	return Config{KnownIndices: m, KnownPDUHashes: hashes, ExpectedUsed: 10}
}
func run(t *testing.T, f *fake, s *sink) (Summary, error) {
	t.Helper()
	p := &Transport{b: f}
	if e := p.Connect(context.Background()); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	s.cancel = cancel
	return p.Listen(ctx, cfg(), s)
}
func TestSnapshotDirectAndEventReadReconcile(t *testing.T) {
	f := &fake{notify: "+CMT: ,1\r\n0000\r\n+CMTI: \"ME\",11\r\n"}
	s := &sink{}
	f.onWrite = func(Command) {
		if len(s.events) == 0 || s.events[len(s.events)-1] != "out_intent" {
			t.Fatal("undurable OUT")
		}
	}
	r, e := run(t, f, s)
	if e != nil || !r.Reconciled || !r.Closed || !s.ready || s.received != 1 || s.snapshots != 1 || s.directs != 1 || f.closes != 1 {
		t.Fatal(r, e, s)
	}
	want := []Command{{1, 0}, {2, 0}, {3, 0}, {4, 0}, {5, 0}, {6, 0}, {7, 11}, {8, 0}, {9, 0}, {10, 0}, {11, 0}}
	if !reflect.DeepEqual(f.commands, want) {
		t.Fatal(f.commands)
	}
}
func TestFailClosed(t *testing.T) {
	for _, name := range []string{"service1", "mode", "store", "snapshot_count", "snapshot_hash", "badcmti", "wrongmemory", "disconnect", "save", "write", "badpdu"} {
		t.Run(name, func(t *testing.T) {
			f := &fake{notify: "+CMTI: \"ME\",11\r\n", override: map[uint8]string{}}
			s := &sink{}
			switch name {
			case "service1":
				f.override[4] = "+CSMS: 1,1,1,1"
			case "mode":
				f.override[1] = "+CMGF: 1"
			case "store":
				f.override[2] = `+CPMS: "SM",11,23,"SM",11,23,"SM",11,23`
			case "snapshot_count":
				f.override[5] = "+CMGL: 0,0,,1\r\n0000"
			case "snapshot_hash":
				var b strings.Builder
				for i := 0; i < 11; i++ {
					fmt.Fprintf(&b, "+CMGL: %d,0,,1\r\n0001\r\n", i)
				}
				f.override[5] = strings.TrimSuffix(b.String(), "\r\n")
			case "badcmti":
				f.notify = "+CMTI: \"ME\",23\r\n"
			case "wrongmemory":
				f.notify = "+CMTI: \"SM\",11\r\n"
			case "disconnect":
				f.disconnect = true
			case "save":
				s.fail = "usb_read"
			case "write":
				f.writeFail = true
			case "badpdu":
				f.override[7] = "+CMGR: 0,,1\r\nOK"
			}
			r, e := run(t, f, s)
			if e == nil || r.Reconciled || f.closes != 1 || s.received != 0 {
				t.Fatal(r, e)
			}
			if name == "service1" {
				for _, c := range f.commands {
					if c.Kind >= 5 {
						t.Fatal("OUT beyond service prerequisite")
					}
				}
			}
		})
	}
}
func TestDirectAllSplitsInsideStoredResponse(t *testing.T) {
	wire := "AT+CMGR=11\r\r\n+CMGR: 0,,1\r\n+CMT: ,2\r\n001234\r\n+CMTI: \"ME\",12\r\n0000\r\nOK\r\n"
	for cut := 1; cut < len(wire); cut++ {
		p := &framer{when: "time"}
		_ = p.begin(Command{7, 11})
		if e := p.feed([]byte(wire[:cut])); e != nil {
			t.Fatal(cut, e)
		}
		if e := p.feed([]byte(wire[cut:])); e != nil {
			t.Fatal(cut, e)
		}
		r, e := p.finish()
		if e != nil || len(r.Messages) != 1 || r.Messages[0].PDU != "0000" || len(p.directs) != 1 || p.directs[0].PDU != "001234" || len(p.notices) != 1 {
			t.Fatal(cut, e, r)
		}
		d := p.directs[0]
		if d.StorageIndex != nil || d.ReportedStatus != nil || d.Header != "+CMT: ,2" || d.PDULineHex != "3030313233340d0a" {
			t.Fatal(d)
		}
	}
}
func TestDirectCannotSupplyEchoOrTerminal(t *testing.T) {
	for _, wire := range []string{"+CMT: ,1\r\nOK\r\n", "+CMT: ,1\r\nAT+CMGR=11\r\n", "+CMT: ,1\r\n+CMT: ,1\r\n", "+CMT: ,999\r\n00\r\n", "+CMT: x\r\n00\r\n", "+CMT: ,1\r\nGG\r\n", "+CMT: ,1\r\n00", "+CMGL: 0,0,,1\r\n0000\r\n", "AT+CMGR=11\r\n+CMGR: 0,,1\r\n0000\r\nOK\r\nOK\r\n"} {
		p := &framer{}
		_ = p.begin(Command{7, 11})
		e := p.feed([]byte(wire))
		if e == nil {
			_, e = p.finish()
		}
		if e == nil {
			t.Fatal(wire)
		}
	}
}
func TestDuplicateCMGLRejectedAndIdleDirect(t *testing.T) {
	p := &framer{}
	_ = p.begin(Command{5, 0})
	e := p.feed([]byte("AT+CMGL=4\r\n+CMGL: 1,0,,1\r\n0000\r\n+CMGL: 1,0,,1\r\n0000\r\nOK\r\n"))
	if e == nil {
		t.Fatal("duplicate")
	}
	p = &framer{}
	if e = p.feed([]byte("+CMT: ,1\r\n0000\r\n")); e != nil || !p.boundary() || len(p.directs) != 1 {
		t.Fatal(e)
	}
}
func TestNativeSurface(t *testing.T) {
	b, _ := os.ReadFile("native.h")
	allowed := map[string]bool{}
	for _, v := range strings.Fields("Release QueryInterface USBInterfaceClose GetNumberOfConfigurations GetConfigurationDescriptorPtr GetInterfaceNumber GetAlternateSetting GetConfigurationValue GetInterfaceClass GetInterfaceSubClass GetInterfaceProtocol GetNumEndpoints GetPipeProperties USBInterfaceOpen WritePipeTO ReadPipeTO") {
		allowed[v] = true
	}
	for _, m := range regexp.MustCompile(`->([A-Za-z0-9_]+)\(`).FindAllStringSubmatch(string(b), -1) {
		if !allowed[m[1]] {
			t.Fatal(m)
		}
	}
	for _, s := range []string{"CMGD", "CNMA", "CMGS", "CMSS", "CMGW", "AT+CSMS=", "AT+CNMI="} {
		if strings.Contains(string(b), s) {
			t.Fatal(s)
		}
	}
	for _, c := range []Command{{7, -1}, {7, 23}, {12, 0}, {0, 0}, {1, 1}} {
		if _, e := c.wire(); e == nil {
			t.Fatal(c)
		}
	}
}
func FuzzNestedDirectFraming(f *testing.F) {
	f.Add([]byte("+CMT: ,1\r\n0000\r\n"), uint8(3))
	f.Fuzz(func(t *testing.T, b []byte, n uint8) {
		if len(b) > 4096 {
			return
		}
		p := &framer{}
		step := int(n)%32 + 1
		for i := 0; i < len(b); i += step {
			end := i + step
			if end > len(b) {
				end = len(b)
			}
			if p.feed(b[i:end]) != nil {
				break
			}
		}
		for _, d := range p.directs {
			if d.StorageIndex != nil || d.ReportedStatus != nil || d.TPDULength < 1 || d.TPDULength > 255 || !isHex(d.PDU) {
				t.Fatal(d)
			}
		}
	})
}

// Requesting a handoff after the first part must finish every already-observed
// CMTI before settings reconciliation. It must not cancel a CMGR mid-response.
type burstSink struct {
	sink
	indices []int
}

func (s *burstSink) Message(n Notice, m smsreceive.RawMessage) error {
	if n.Index != m.Index {
		return errors.New("wrong burst index")
	}
	s.indices = append(s.indices, m.Index)
	return nil
}
func TestYieldDrainsQueuedBurstBeforeReconciliation(t *testing.T) {
	f := &fake{notify: "+CMTI: \"ME\",11\r\n+CMTI: \"ME\",12\r\n+CMTI: \"ME\",13\r\n", override: map[uint8]string{8: `+CPMS: "ME",14,23,"ME",14,23,"ME",14,23`}}
	s := &burstSink{}
	p := &Transport{b: f}
	if e := p.Connect(context.Background()); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	config := cfg()
	config.Yield = func() bool { return len(s.indices) > 0 }
	result, e := p.Listen(ctx, config, s)
	if e != nil || !result.Reconciled || !result.Closed || !reflect.DeepEqual(s.indices, []int{11, 12, 13}) {
		t.Fatal(result, s.indices, e)
	}
	count := 0
	for _, cmd := range f.commands {
		if cmd.Kind == 7 {
			count++
		}
		if cmd.Kind == 8 && count != 3 {
			t.Fatal("handoff before all observed parts")
		}
	}
}

func TestIdleInterruptionRequiresDurableCleanBoundary(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		code                           uint32
		query, closeFail, removedClose bool
		fail, fragment                 string
		recover                        bool
	}{
		{name: "abort", code: 0xe00002eb, recover: true},
		{name: "removed", code: 0xe00002c0, recover: true},
		{name: "not_attached", code: 0xe00002d9, recover: true},
		{name: "not_responding_without_removal_proof", code: 0xe00002ed},
		{name: "not_responding_removed", code: 0xe00002ed, removedClose: true, recover: true},
		{name: "abort_removed", code: 0xe00002eb, removedClose: true, recover: true},
		{name: "removed_in_query", code: 0xe00002ed, query: true, removedClose: true},
		{name: "removed_partial", code: 0xe00002ed, fragment: "+CMTI:", removedClose: true},
		{name: "removed_end_failure", code: 0xe00002ed, removedClose: true, fail: "session_end"},
		{name: "removed_read_failure", code: 0xe00002ed, removedClose: true, fail: "usb_read"},
		{name: "other_io_error", code: 0xe00002bc},
		{name: "active_query", code: 0xe00002eb, query: true},
		{name: "partial_line", code: 0xe00002eb, fragment: "+CMTI:"},
		{name: "partial_direct", code: 0xe00002eb, fragment: "+CMT: ,1\r\n"},
		{name: "end_not_durable", code: 0xe00002eb, fail: "session_end"},
		{name: "read_not_durable", code: 0xe00002eb, fail: "usb_read"},
		{name: "close_failure", code: 0xe00002eb, closeFail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{faultAfter: 6, faultCode: tc.code, faultIdle: !tc.query, notify: tc.fragment}
			if tc.closeFail {
				f.closeErr = errors.New("close failed")
			}
			if tc.removedClose {
				f.closeErr = &releasedDeviceGone{42}
			}
			s := &sink{fail: tc.fail}
			r, e := run(t, f, s)
			if e == nil || r.Interrupted != tc.recover || r.Reconciled || f.closes != 1 {
				t.Fatalf("summary=%+v error=%v closes=%d", r, e, f.closes)
			}
			for _, cmd := range f.commands {
				if cmd.Kind > 6 {
					t.Fatalf("OUT after interruption: %+v", f.commands)
				}
			}
		})
	}
}

func TestReleasedCloseRequiresPinnedAbsenceAndExactNoDevice(t *testing.T) {
	for _, tc := range []struct {
		name    string
		code    uint32
		id      uint64
		present []uint64
		scanErr error
		gone    bool
	}{
		{"absent", 0xe00002c0, 42, nil, nil, true},
		{"new_identity", 0xe00002c0, 42, []uint64{43}, nil, true},
		{"same_device", 0xe00002c0, 42, []uint64{42}, nil, false},
		{"unknown_identity", 0xe00002c0, 0, nil, nil, false},
		{"scan_failed", 0xe00002c0, 42, nil, errors.New("scan"), false},
		{"other_close_error", 0xe00002bc, 42, nil, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := reviewedReleasedClose(tc.code, tc.id, tc.present, tc.scanErr)
			var g *releasedDeviceGone
			if errors.As(e, &g) != tc.gone {
				t.Fatal(e)
			}
		})
	}
}
