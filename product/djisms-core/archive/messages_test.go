package archive

import (
	"fmt"
	"strings"
	"testing"
)

func TestMessageDedupAndNotificationFailurePreservesEligibility(t *testing.T) {
	s, e := Open(privateTestRoot(t))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	a, e := s.Ingest("first", "0000", meta(), M{"state": "readable", "sender": "sender", "text": "你好 100%"})
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Ingest("repeat", "0000", meta(), M{"state": "readable", "sender": "sender", "text": "你好 100%"})
	if e != nil {
		t.Fatal(e)
	}
	first, e := s.Materialize([]string{a}, "complete", "sender", "你好 100%", true)
	if e != nil {
		t.Fatal(e)
	}
	repeat, e := s.Materialize([]string{b}, "complete", "sender", "你好 100%", true)
	if e != nil || repeat != first {
		t.Fatal("duplicate", e)
	}
	p, e := s.PendingNotifications()
	if e != nil || len(p) != 1 {
		t.Fatal(p, e)
	}
	if e = s.NotificationResult(first, "failed", "test denied"); e != nil {
		t.Fatal(e)
	}
	if ok, e := s.Eligible(a); e != nil || !ok {
		t.Fatal("notification broke eligibility", e)
	}
	p, e = s.PendingNotifications()
	if e != nil || len(p) != 0 {
		t.Fatal("failure loops", e)
	}
	for _, search := range []string{"你好", "100%", "sender"} {
		ms, e := s.Messages(search, 20, 0)
		if e != nil || len(ms) != 1 {
			t.Fatal(ms, e)
		}
	}
	ms, e := s.Messages("_", 20, 0)
	if e != nil || len(ms) != 0 {
		t.Fatal("search wildcard", e)
	}
}
func TestHistoryDoesNotBecomeNewNotification(t *testing.T) {
	s, e := Open(privateTestRoot(t))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	a, e := s.Ingest("history", "0000", meta(), M{"state": "readable"})
	if e != nil {
		t.Fatal(e)
	}
	id, e := s.Materialize([]string{a}, "complete", "sender", "old", false)
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Ingest("snapshot", "0000", meta(), M{"state": "readable"})
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.Materialize([]string{b}, "complete", "sender", "old", true)
	if e != nil || again != id {
		t.Fatal(again, e)
	}
	p, e := s.PendingNotifications()
	if e != nil || len(p) != 0 {
		t.Fatal(p, e)
	}
}

func TestHistoryPaginationAndDistinctMessageTimes(t *testing.T) {
	s, e := Open(privateTestRoot(t))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	body := strings.Repeat("中文长正文", 100)
	for n := 0; n < 205; n++ {
		rid, e := s.Ingest(fmt.Sprintf("history/%d", n), fmt.Sprintf("00%04x", n), meta(), M{"state": "readable", "sender": "sender", "text": body, "timestamp": "2026-09-18T03:00:00Z"})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.Materialize([]string{rid}, "complete", "sender", body, false); e != nil {
			t.Fatal(e)
		}
	}
	seen := map[string]bool{}
	for page, want := range []int{100, 100, 5} {
		rows, e := s.Messages("中文", 100, page*100)
		if e != nil || len(rows) != want {
			t.Fatal(len(rows), e)
		}
		for _, m := range rows {
			if seen[m.ID] || len([]rune(m.Body)) != 300 || m.SentAt != "2026-09-18T03:00:00Z" || m.SentAt == m.CreatedAt {
				t.Fatal("pagination/time/truncation mismatch", m.ID)
			}
			seen[m.ID] = true
		}
		full, e := s.Message(rows[0].ID)
		if e != nil || full.Body != body {
			t.Fatal("full body lost", e)
		}
	}
	if len(seen) != 205 {
		t.Fatal("missing history rows")
	}
	if rows, e := s.PendingNotifications(); e != nil || len(rows) != 0 {
		t.Fatal("import generated notifications", e)
	}
}

func TestReceivingSIMIsReadOnlyProjectionAndOldHistoryStaysUnknown(t *testing.T) {
	s, e := Open(privateTestRoot(t))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	m := meta()
	m["sim_id"] = "stable-card-a"
	m["sim_number"] = "+8613800000000"
	rid, e := s.Ingest("sim-meta", "0000", m, M{"state": "readable"})
	if e != nil {
		t.Fatal(e)
	}
	id, e := s.Materialize([]string{rid}, "complete", "10010", "test", true)
	if e != nil {
		t.Fatal(e)
	}
	before, e := s.Summary()
	if e != nil {
		t.Fatal(e)
	}
	one, e := s.Message(id)
	if e != nil || one.SIMID != "stable-card-a" || one.SIMNumber != "+8613800000000" {
		t.Fatal(one, e)
	}
	list, e := s.Messages("10010", 20, 0)
	if e != nil || len(list) != 1 || list[0].SIMID != one.SIMID {
		t.Fatal(list, e)
	}
	after, e := s.Summary()
	if e != nil || fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatal("read projection changed archive")
	}
	if row := messageRow(nil); row.SIMID != "" || row.SIMNumber != "" {
		t.Fatal("legacy guessed")
	}
}
