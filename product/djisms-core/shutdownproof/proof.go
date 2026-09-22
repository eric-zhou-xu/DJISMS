// Package shutdownproof proves only closed, zero-OUT, zero-input status cancellation.
package shutdownproof

import (
	"errors"
	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"github.com/iniwex5/vohive/product/djisms-core/buildinfo"
	"github.com/iniwex5/vohive/product/djisms-core/status"
	"strings"
)

const Scope = "closed_zero_out_status_cancel_v1"

type Review struct {
	Session string `json:"session"`
	Stop    string `json:"stop_fingerprint"`
	Query   string `json:"query_sha256"`
	Host    string `json:"host_sha256"`
	Build   string `json:"build_id"`
	Source  string `json:"source_sha256"`
	Note    string `json:"note"`
}

func Plan(p status.PlanReport) bool {
	if !p.CloseSucceeded || p.Error != "context canceled" || p.Policy != "readonly-status-demux-v2" || len(p.Queries) != 1 {
		return false
	}
	q := p.Queries[0]
	return Query(q) && q.StopReason == "context canceled" && q.Outcome == "FAIL"
}
func Query(q status.Report) bool {
	if q.Command != "AT+CGMI" || q.Interface != 2 || q.RequestID == 0 || q.Policy != "readonly-status-demux-v2" || q.PlannedOutHex != "41542b43474d490d" || q.WriteAttempted || q.WriteSucceeded || q.Success || q.EchoMatched || q.TerminalOK || len(q.Events) > 0 || len(q.Frames) > 0 || len(q.ResponseLines) > 0 || len(q.Reads) > 1 {
		return false
	}
	for _, r := range q.Reads {
		d := r.Diagnostic
		if r.Phase != "before_write" || r.StreamOffset != 0 || r.ValidHex != "" || d.CountValid || d.ActualBytes != nil || d.Category != "timeout" || (d.ReturnCode != 0xe0004051 && d.ReturnCode != 0xe00002d6) {
			return false
		}
	}
	return true
}
func candidate(r Review) bool {
	return r.Build == buildinfo.BuildID && r.Source == buildinfo.SourceTree && r.Build != "unversioned-development" && len(r.Source) == 64 && strings.Trim(r.Source, "0123456789abcdef") == ""
}
func Fingerprint(v archive.M) string { b, _ := archive.Canonical(v); return archive.Hash(b) }
func Verify(s *archive.Store, r Review) error {
	if !candidate(r) || len(r.Note) < 20 {
		return errors.New("explicit candidate-bound cancellation review required")
	}
	stop, e := s.LatestEvent("core_safety_stop")
	if e != nil {
		return e
	}
	if stop == nil || stop["error"] != "context canceled" || Fingerprint(stop) != r.Stop {
		return errors.New("not exact reviewed cancellation STOP")
	}
	if e = s.StatusRecoveryContext(r.Session, r.Query, r.Host); e != nil {
		return e
	}
	total, e := s.Summary()
	if e != nil {
		return e
	}
	if total["unfinished_delete_attempts"] != 0 || total["pending_temp_files"] != 0 {
		return errors.New("unresolved archive/destructive state")
	}
	closedEvents, e := s.EventsOfKind("status_session_closed")
	if e != nil {
		return e
	}
	auditEvents, e := s.EventsOfKind("status_handoff_audited")
	if e != nil {
		return e
	}
	var closed archive.M
	audited := false
	for _, v := range closedEvents {
		if v["session"] == r.Session {
			closed = v
		}
	}
	for _, v := range auditEvents {
		if v["session"] == r.Session {
			audited = true
		}
	}
	if closed == nil || closed["closed"] != true || !audited {
		return errors.New("closed audited session proof missing")
	}
	facts, e := s.SourceFacts("session/" + r.Session + "/")
	if e != nil {
		return e
	}
	foundQ, foundEnd := false, false
	var request uint64
	for _, f := range facts {
		switch {
		case strings.HasSuffix(f.Name, "/host_before"):
			if f.Hash != r.Host {
				return errors.New("host identity changed")
			}
		case strings.HasSuffix(f.Name, "/status_query"):
			var q status.Report
			var raw archive.M
			if archive.Decode(f.Data, &q) != nil || archive.Decode(f.Data, &raw) != nil || raw["write_attempted"] != false || raw["write_succeeded"] != false || !Query(q) {
				return errors.New("nonzero OUT/input or incomplete query proof")
			}
			if request != 0 && request != q.RequestID {
				return errors.New("multiple requests")
			}
			request = q.RequestID
			if f.Hash == r.Query {
				foundQ = q.StopReason == "context canceled"
			}
		case strings.HasSuffix(f.Name, "/status_complete"):
			var p status.PlanReport
			if archive.Decode(f.Data, &p) != nil || !Plan(p) || p.Queries[0].RequestID != request {
				return errors.New("closure/cancel plan proof invalid")
			}
			foundEnd = true
		default:
			return errors.New("unexpected transport source in cancellation session")
		}
	}
	if !foundQ || !foundEnd {
		return errors.New("incomplete cancellation evidence")
	}
	return nil
}
func Authorize(s *archive.Store, r Review) error {
	if e := Verify(s, r); e != nil {
		return e
	}
	b, e := archive.Canonical(r)
	if e != nil {
		return e
	}
	h, e := s.SaveSource("closed-cancel-review/"+r.Stop, b)
	if e != nil {
		return e
	}
	return s.Event("closed_cancel_recovery_authorized", "", archive.M{"scope": Scope, "stop_fingerprint": r.Stop, "build_id": r.Build, "source_sha256": r.Source, "proof_sha256": h})
}
func Allowed(s *archive.Store) (bool, error) {
	g, e := s.LatestEvent("closed_cancel_recovery_authorized")
	if e != nil || g == nil {
		return false, e
	}
	stop, e := s.LatestEvent("core_safety_stop")
	if e != nil || stop == nil {
		return false, e
	}
	if g["scope"] != Scope || g["stop_fingerprint"] != Fingerprint(stop) || g["build_id"] != buildinfo.BuildID || g["source_sha256"] != buildinfo.SourceTree {
		return false, nil
	}
	facts, e := s.SourceFacts("closed-cancel-review/" + Fingerprint(stop))
	if e != nil {
		return false, e
	}
	for _, f := range facts {
		if f.Hash == g["proof_sha256"] {
			var r Review
			if e = archive.Decode(f.Data, &r); e != nil {
				return false, e
			}
			e = Verify(s, r)
			return e == nil, e
		}
	}
	return false, errors.New("cancellation proof missing")
}
