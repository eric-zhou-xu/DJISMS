package simstore

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/iniwex5/vohive/internal/safeusb"
	"github.com/iniwex5/vohive/internal/smsreceive"
	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"github.com/iniwex5/vohive/product/djisms-core/discovery"
	"github.com/warthog618/sms/encoding/tpdu"
	"github.com/warthog618/sms/encoding/ucs2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

type fakeSM struct {
	sms                                  map[int]smsreceive.RawMessage
	mem                                  string
	pending                              []byte
	writes                               []Command
	sim                                  string
	failDelete, changeOther, failRestore bool
}

func (f *fakeSM) snapshot() (safeusb.Snapshot, error) { return safeusb.Snapshot{}, nil }
func (f *fakeSM) open() error                         { return nil }
func (f *fakeSM) close() error                        { return nil }
func (f *fakeSM) write(c Command, _ time.Duration) error {
	f.writes = append(f.writes, c)
	w, e := c.wire()
	if e != nil {
		return e
	}
	body := ""
	switch c.Kind {
	case 1:
		body = "+CMGF: 0"
	case 2:
		if f.mem == "SM" {
			body = fmt.Sprintf(`+CPMS: "SM",%d,50,"ME",0,23,"ME",0,23`, len(f.sms))
		} else {
			body = `+CPMS: "ME",0,23,"ME",0,23,"ME",0,23`
		}
	case 3:
		body = "+CNMI: 2,1,0,0,0"
	case 4:
		body = "+QCCID: " + f.sim
	case 10:
		body = `+CPMS: ("ME","SM"),("ME","SM"),("ME","SM")`
	case 6:
		f.mem = "SM"
		body = fmt.Sprintf("+CPMS: %d,50,0,23,0,23", len(f.sms))
	case 8:
		if f.failRestore {
			return errors.New("restore write interrupted")
		}
		f.mem = "ME"
		body = "+CPMS: 0,23,0,23,0,23"
	case 5:
		ks := []int{}
		for i := range f.sms {
			ks = append(ks, i)
		}
		sort.Ints(ks)
		for _, i := range ks {
			m := f.sms[i]
			body += fmt.Sprintf("+CMGL: %d,%d,,%d\r\n%s\r\n", i, m.Status, m.TPDULength, m.PDU)
		}
	case 7:
		m, ok := f.sms[c.Index]
		if !ok {
			return errors.New("missing fake index")
		}
		body = fmt.Sprintf("+CMGR: %d,,%d\r\n%s", m.Status, m.TPDULength, m.PDU)
	case 9:
		if f.failDelete {
			return errors.New("uncertain delete result")
		}
		delete(f.sms, c.Index)
		if f.changeOther {
			for i, m := range f.sms {
				m.Status = 1 - m.Status
				f.sms[i] = m
				break
			}
		}
	}
	f.pending = []byte(w + "\r\r\n" + strings.TrimSuffix(body, "\r\n") + "\r\nOK\r\n")
	return nil
}
func (f *fakeSM) readDiagnostic(b []byte, _ time.Duration) (ReadDiagnostic, error) {
	if len(f.pending) == 0 {
		return classifyRead(ioTimeout, uint32(len(b)), uint32(len(b))), errTimeout
	}
	n := copy(b, f.pending)
	f.pending = f.pending[n:]
	return classifyRead(0, uint32(n), uint32(len(b))), nil
}
func (f *fakeSM) opener(_ context.Context) (*Transport, discovery.Device, error) {
	return &Transport{b: f, connected: true}, discovery.Device{RegistryID: 123, Location: 456}, nil
}
func synthetic(t *testing.T, idx int) smsreceive.RawMessage {
	p, e := tpdu.NewDeliver()
	if e != nil {
		t.Fatal(e)
	}
	p.OA = tpdu.NewAddress(tpdu.FromNumber("+8613800138000"))
	p.SCTS = tpdu.Timestamp{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	p.DCS = tpdu.DCS(8)
	p.UD = ucs2.Encode([]rune(fmt.Sprintf("synthetic SM %d", idx)))
	b, e := p.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	return smsreceive.RawMessage{Index: idx, Status: 1, TPDULength: len(b), PDU: "00" + hex.EncodeToString(b)}
}
func fixtureSM(t *testing.T) (*archive.Store, *fakeSM) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	s, e := archive.Open(root)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s, &fakeSM{sms: map[int]smsreceive.RawMessage{1: synthetic(t, 1), 8: synthetic(t, 8)}, mem: "ME", sim: "89860000000000000000"}
}
func deletes(f *fakeSM) int {
	n := 0
	for _, c := range f.writes {
		if c.Kind == 9 {
			n++
		}
	}
	return n
}
func TestPlanThenExactMultiDelete(t *testing.T) {
	s, f := fixtureSM(t)
	p, e := run(context.Background(), s, Request{Action: "plan", Indices: []int{1, 8}}, f.opener)
	if e != nil || !p.Restored || p.Plan == "" || len(p.Items) != 2 || !p.Items[0].Archived || deletes(f) != 0 {
		t.Fatal(p, e)
	}
	v, e := run(context.Background(), s, Request{Action: "delete", Plan: p.Plan, Confirmed: true}, f.opener)
	if e != nil || !v.Restored || len(v.Items) != 0 || deletes(f) != 2 {
		t.Fatal(v, e)
	}
	if _, e = run(context.Background(), s, Request{Action: "delete", Plan: p.Plan, Confirmed: true}, f.opener); e == nil {
		t.Fatal("plan replay allowed")
	}
	summary, _ := s.Summary()
	if summary["unfinished_delete_attempts"] != 0 {
		t.Fatal(summary)
	}
}
func TestDestructiveFailClosed(t *testing.T) {
	for _, mode := range []string{"no_confirmation", "wrong_sim", "changed_target", "other_changed", "uncertain_delete", "restore_interrupted"} {
		t.Run(mode, func(t *testing.T) {
			s, f := fixtureSM(t)
			p, e := run(context.Background(), s, Request{Action: "plan", Indices: []int{1, 8}}, f.opener)
			if e != nil {
				t.Fatal(e)
			}
			req := Request{Action: "delete", Plan: p.Plan, Confirmed: true}
			switch mode {
			case "no_confirmation":
				req.Confirmed = false
			case "wrong_sim":
				f.sim = "89860000000000000001"
			case "changed_target":
				f.sms[1] = synthetic(t, 2)
			case "other_changed":
				f.changeOther = true
			case "uncertain_delete":
				f.failDelete = true
			case "restore_interrupted":
				f.failRestore = true
			}
			_, e = run(context.Background(), s, req, f.opener)
			if e == nil {
				t.Fatal("fault passed")
			}
			if mode == "no_confirmation" || mode == "wrong_sim" || mode == "changed_target" {
				if deletes(f) != 0 {
					t.Fatal("unauthorized delete")
				}
			}
			if mode == "other_changed" || mode == "uncertain_delete" {
				if deletes(f) != 1 {
					t.Fatal("batch continued")
				}
				summary, _ := s.Summary()
				if summary["unfinished_delete_attempts"] == 0 {
					t.Fatal("unknown delete lost")
				}
			}
			if mode == "restore_interrupted" && Pending(s) == nil {
				t.Fatal("interrupted selection disappeared")
			}
		})
	}
}
func TestEmptyDuplicateAndUnarchivableSelections(t *testing.T) {
	for _, indices := range [][]int{{}, {1, 1}, {99}} {
		s, f := fixtureSM(t)
		_, e := run(context.Background(), s, Request{Action: "plan", Indices: indices}, f.opener)
		if e == nil || deletes(f) != 0 {
			t.Fatal("invalid plan accepted")
		}
	}
	s, f := fixtureSM(t)
	f.sms[1] = smsreceive.RawMessage{Index: 1, Status: 0, TPDULength: 1, PDU: "0000"}
	_, e := run(context.Background(), s, Request{Action: "plan", Indices: []int{1}}, f.opener)
	if e == nil || deletes(f) != 0 {
		t.Fatal("unarchivable target allowed")
	}
}
func TestNewCoreStopBlocksStorage(t *testing.T) {
	s, f := fixtureSM(t)
	s.Event("core_safety_stop", "", archive.M{"error": "new integrity fault"})
	_, e := run(context.Background(), s, Request{Action: "inspect"}, f.opener)
	if e == nil || len(f.writes) != 0 {
		t.Fatal("STOP bypassed")
	}
}

func TestInterruptedSelectionSurvivesReopen(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	os.Chmod(root, 0700)
	s, e := archive.Open(root)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Event("sim_selection_intent", "", archive.M{"session": "crash-fixture"}); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = archive.Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	opened := false
	_, e = run(context.Background(), s, Request{Action: "check"}, func(context.Context) (*Transport, discovery.Device, error) {
		opened = true
		return nil, discovery.Device{}, nil
	})
	if e == nil || opened {
		t.Fatal("restart bypassed pending storage selection")
	}
}
func TestSIMDisplayFieldsFromArchivedPDU(t *testing.T) {
	s, f := fixtureSM(t)
	r, e := run(context.Background(), s, Request{Action: "inspect"}, f.opener)
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range r.Items {
		if v.Sender != "+8613800138000" || !strings.Contains(v.Body, "synthetic SM") || v.Time == "" || !v.Archived {
			t.Fatal(v)
		}
	}
}

func TestDirectDuringSMIsArchivedAndNotificationQueued(t *testing.T) {
	s, _ := fixtureSM(t)
	v := synthetic(t, 1)
	h, e := s.SaveSource("sim-test/direct", []byte("synthetic fixture transport"))
	if e != nil {
		t.Fatal(e)
	}
	e = preserveDirect(s, "synthetic-direct", Direct{EventID: 1, ObservedUTC: time.Now().UTC().Format(time.RFC3339Nano), PDU: v.PDU, TPDULength: v.TPDULength}, h)
	if e != nil {
		t.Fatal(e)
	}
	notices, e := s.PendingNotifications()
	if e != nil || len(notices) != 1 {
		t.Fatal(notices, e)
	}
	done, e := s.EventsOfKind("direct_handoff_complete")
	if e != nil || len(done) != 1 {
		t.Fatal(done, e)
	}
}
