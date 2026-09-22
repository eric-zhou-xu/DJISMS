package decoder

import (
	"encoding/hex"
	"fmt"
	"github.com/iniwex5/vohive/internal/smsreceive"
	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"github.com/warthog618/sms/encoding/tpdu"
	"github.com/warthog618/sms/encoding/ucs2"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testStore(t *testing.T) *archive.Store {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	os.Chmod(root, 0700)
	s, e := archive.Open(root)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func fixture(t *testing.T, text string, seq, total int, binary bool) smsreceive.RawMessage {
	p, e := tpdu.NewDeliver()
	if e != nil {
		t.Fatal(e)
	}
	p.OA = tpdu.NewAddress(tpdu.FromNumber("+15550000000"))
	p.SCTS = tpdu.Timestamp{Time: time.Date(2026, 9, 18, 3, 0, 0, 0, time.UTC)}
	p.DCS = 8
	p.UD = ucs2.Encode([]rune(text))
	if binary {
		p.DCS = 4
		p.UD = []byte{0, 255, 1}
	}
	if total > 0 {
		p.FirstOctet |= 0x40
		p.UDH = tpdu.UserDataHeader{{ID: 0, Data: []byte{7, byte(total), byte(seq)}}}
	}
	b, e := p.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	return smsreceive.RawMessage{Index: seq, TPDULength: len(b), PDU: "00" + hex.EncodeToString(b)}
}
func ingest(t *testing.T, s *archive.Store, key string, m smsreceive.RawMessage) string {
	t.Helper()
	id, e := s.Ingest(key, m.PDU, archive.M{"device_key": "fixture", "connection_id": "test", "storage": "ME", "index": m.Index, "received_at": "2026-09-18T03:00:00Z", "original_metadata": m}, nil)
	if e != nil {
		t.Fatal(e)
	}
	return id
}
func TestMultipartAcrossArchivedSlotsAndRestart(t *testing.T) {
	s := testStore(t)
	a := ingest(t, s, "part-a", fixture(t, "第一段", 1, 2, false))
	if e := Process(s, []string{a}, true); e != nil {
		t.Fatal(e)
	}
	pending, e := s.PendingNotifications()
	if e != nil || len(pending) != 0 {
		t.Fatal("partial notification", e)
	}
	root := s.Root
	s.Close()
	s, e = archive.Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	b := ingest(t, s, "part-b", fixture(t, "第二段", 2, 2, false))
	if e = Process(s, []string{b}, true); e != nil {
		t.Fatal(e)
	}
	pending, e = s.PendingNotifications()
	if e != nil || len(pending) != 1 || pending[0].Body != "第一段第二段" {
		t.Fatal(pending, e)
	}
	for _, id := range []string{a, b} {
		if ok, e := s.Eligible(id); e != nil || !ok {
			t.Fatal("fragment not retained eligible", e)
		}
	}
	// Repeated snapshots must not create a second notification.
	for i, m := range []smsreceive.RawMessage{fixture(t, "第一段", 1, 2, false), fixture(t, "第二段", 2, 2, false)} {
		id := ingest(t, s, fmt.Sprintf("repeat-%d", i), m)
		if e = Process(s, []string{id}, true); e != nil {
			t.Fatal(e)
		}
	}
	pending, e = s.PendingNotifications()
	if e != nil || len(pending) != 1 {
		t.Fatal("duplicate notification", pending, e)
	}
}
func TestBinaryMalformedAndClass0(t *testing.T) {
	s := testStore(t)
	for i, m := range []smsreceive.RawMessage{fixture(t, "", 0, 0, true), {Index: 1, TPDULength: 99, PDU: "0000"}} {
		id := ingest(t, s, fmt.Sprintf("fixture-%d", i), m)
		if e := Process(s, []string{id}, true); e != nil {
			t.Fatal(e)
		}
	}
	pending, e := s.PendingNotifications()
	if e != nil || len(pending) != 0 {
		t.Fatal("invented text", e)
	}
	m := fixture(t, "即时验证码", 0, 0, false)
	raw, _ := hex.DecodeString(m.PDU)
	p := tpdu.TPDU{}
	if e = p.UnmarshalBinary(raw[1:]); e != nil {
		t.Fatal(e)
	}
	p.DCS = 0x18
	b, e := p.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	m.PDU = "00" + hex.EncodeToString(b)
	m.TPDULength = len(b)
	id, e := s.Ingest("class0", m.PDU, archive.M{"device_key": "fixture", "connection_id": "test", "storage": nil, "index": nil, "received_at": "2026-09-18T03:00:00Z", "original_metadata": m}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = Process(s, []string{id}, true); e != nil {
		t.Fatal(e)
	}
	pending, e = s.PendingNotifications()
	if e != nil || len(pending) != 1 || pending[0].Body != "即时验证码" {
		t.Fatal(pending, e)
	}
	if ok, e := s.Eligible(id); e != nil || ok {
		t.Fatal("direct event has fake slot", e)
	}
}
