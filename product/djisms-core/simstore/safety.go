package simstore

import (
	"errors"
	"github.com/iniwex5/vohive/product/djisms-core/archive"
)

// This companion ships with the byte-identical accepted R8 Core. It cannot grant
// recovery or borrow authorization from a different Core or later STOP.
const coreBuild = "20260922-v1-final-r8-01"
const coreSource = "8b35d87af8cfdb7ff505666e81db7cacb733bba53e86b2c7d70268a05a0dcc5c"

func baselineSafety(s *archive.Store) error {
	stop, e := s.LatestEvent("core_safety_stop")
	if e != nil || stop == nil {
		return e
	}
	raw, e := archive.Canonical(stop)
	if e != nil {
		return e
	}
	fp := archive.Hash(raw)
	grant, e := s.LatestEvent("status_recovery_authorized")
	if e != nil {
		return e
	}
	if grant == nil || grant["stop_fingerprint"] != fp || grant["build_id"] != coreBuild || grant["source_tree_sha256"] != coreSource || grant["scope"] != "proven_status_request_continuation_v1" {
		return errors.New("unreviewed Core STOP; SIM management prohibited")
	}
	facts, e := s.SourceFacts("status-recovery/" + fp)
	if e != nil {
		return e
	}
	for _, f := range facts {
		if f.Hash == grant["evidence_sha256"] {
			var p archive.M
			if e = archive.Decode(f.Data, &p); e != nil {
				return e
			}
			review, ok := p["review"].(map[string]any)
			if ok && p["reconciled"] == true && review["stop_fingerprint"] == fp && review["build_id"] == coreBuild && review["source_tree_sha256"] == coreSource {
				return nil
			}
		}
	}
	return errors.New("Core recovery proof missing; no implicit clear STOP")
}
