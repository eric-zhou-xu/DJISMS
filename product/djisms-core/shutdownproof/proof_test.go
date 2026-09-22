package shutdownproof

import (
	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"github.com/iniwex5/vohive/product/djisms-core/buildinfo"
	"github.com/iniwex5/vohive/product/djisms-core/status"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sample() status.PlanReport {
	return status.PlanReport{CloseSucceeded: true, Error: "context canceled", Policy: "readonly-status-demux-v2", Queries: []status.Report{{Command: "AT+CGMI", Interface: 2, RequestID: 1, PlannedOutHex: "41542b43474d490d", Policy: "readonly-status-demux-v2", StopReason: "context canceled", Outcome: "FAIL"}}}
}
func TestZeroOUTPlanRejectsUnsafeEvidence(t *testing.T) {
	if !Plan(sample()) {
		t.Fatal("safe plan rejected")
	}
	for _, mutate := range []func(*status.PlanReport){func(p *status.PlanReport) { p.CloseSucceeded = false }, func(p *status.PlanReport) { p.Queries[0].WriteAttempted = true }, func(p *status.PlanReport) { p.Queries[0].WriteSucceeded = true }, func(p *status.PlanReport) { p.Error = "unknown fault" }, func(p *status.PlanReport) { p.Queries[0].Command = "AT+CMGD=1" }, func(p *status.PlanReport) { p.Queries[0].ResponseLines = []string{"unexpected"} }, func(p *status.PlanReport) { p.Queries[0].Reads = []status.ReadEvidence{{ValidHex: "01"}} }, func(p *status.PlanReport) { p.Queries = nil }} {
		p := sample()
		mutate(&p)
		if Plan(p) {
			t.Fatal("unsafe plan accepted", p)
		}
	}
}
func TestExplicitReviewBindingAndReopen(t *testing.T) {
	oldB, oldS := buildinfo.BuildID, buildinfo.SourceTree
	buildinfo.BuildID = "fixture-fix"
	buildinfo.SourceTree = strings.Repeat("a", 64)
	defer func() { buildinfo.BuildID = oldB; buildinfo.SourceTree = oldS }()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	os.Chmod(root, 0700)
	s, e := archive.Open(root)
	if e != nil {
		t.Fatal(e)
	}
	session := strings.Repeat("b", 32)
	prefix := "session/" + session + "/"
	host, _ := s.SaveSource(prefix+"000001/host_before", []byte("{}"))
	p := sample()
	b, _ := archive.Canonical(p.Queries[0])
	q, _ := s.SaveSource(prefix+"000002/status_query", b)
	b, _ = archive.Canonical(p)
	s.SaveSource(prefix+"000003/status_complete", b)
	s.Event("status_session_closed", "", archive.M{"session": session, "closed": true})
	s.Event("status_handoff_audited", "", archive.M{"session": session})
	stop := archive.M{"error": "context canceled", "when": "fixture"}
	s.Event("core_safety_stop", "", stop)
	r := Review{session, Fingerprint(stop), q, host, buildinfo.BuildID, buildinfo.SourceTree, "Explicit synthetic zero-OUT cancellation review"}
	if ok, _ := Allowed(s); ok {
		t.Fatal("automatic recovery")
	}
	if e = Authorize(s, r); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = archive.Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	s.Event("status_session_closed", "", archive.M{"session": "later-safe-session", "closed": true})
	if ok, e := Allowed(s); !ok || e != nil {
		t.Fatal(ok, e)
	}
	bad := r
	bad.Query = strings.Repeat("f", 64)
	if Verify(s, bad) == nil {
		t.Fatal("stale source accepted")
	}
	s.Event("core_safety_stop", "", archive.M{"error": "new integrity fault"})
	if ok, _ := Allowed(s); ok {
		t.Fatal("new STOP bypassed")
	}
}
