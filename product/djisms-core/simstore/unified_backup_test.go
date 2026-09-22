package simstore

import (
	"context"
	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"github.com/iniwex5/vohive/product/djisms-core/decoder"
	"testing"
)

func TestUnifiedLocalBackup(t *testing.T) {
	s, f := fixtureSM(t)
	me := synthetic(t, 99)
	rid, e := s.Ingest("synthetic/ME/99", me.PDU, archive.M{"device_key": "synthetic-device", "connection_id": "synthetic-connection", "received_at": "2026-01-01T00:00:00Z", "storage": "ME", "index": 99, "original_metadata": me}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = decoder.Process(s, []string{rid}, false); e != nil {
		t.Fatal(e)
	}
	if _, e = run(context.Background(), s, Request{Action: "inspect"}, f.opener); e != nil {
		t.Fatal(e)
	}
	rows, e := s.Messages("", 200, 0)
	if e != nil || len(rows) != 3 {
		t.Fatal(rows, e)
	}
	for _, r := range rows {
		if r.Sender != "+8613800138000" || r.Body == "" {
			t.Fatal(r)
		}
	}
	if deletes(f) != 0 {
		t.Fatal("backup deleted SIM")
	}
	if _, e = run(context.Background(), s, Request{Action: "inspect"}, f.opener); e != nil {
		t.Fatal(e)
	}
	rows, e = s.Messages("", 200, 0)
	if e != nil || len(rows) != 3 {
		t.Fatal("duplicate backup messages", rows, e)
	}
}
