package runtime

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"github.com/iniwex5/vohive/product/djisms-core/buildinfo"
	"github.com/iniwex5/vohive/product/djisms-core/discovery"
	"github.com/iniwex5/vohive/product/djisms-core/host"
	"github.com/iniwex5/vohive/product/djisms-core/status"
)

const statusRecoveryScope = "proven_status_request_continuation_v1"

type StatusRecoveryReview struct {
	StopFingerprint string `json:"stop_fingerprint"`
	Session         string `json:"session"`
	ReportSource    string `json:"report_source_sha256"`
	HostSource      string `json:"host_source_sha256"`
	BuildID         string `json:"build_id"`
	SourceTree      string `json:"source_tree_sha256"`
	Note            string `json:"operator_review"`
}

func (r StatusRecoveryReview) candidateOK() bool {
	return validCandidate(DNSRecoveryReview{BuildID: r.BuildID, SourceTree: r.SourceTree})
}
func (c *Core) reviewedStatusStop(stop archive.M) (bool, error) {
	grant, e := c.Store.LatestEvent("status_recovery_authorized")
	if e != nil || grant == nil {
		return false, e
	}
	fp, e := stopFingerprint(stop)
	if e != nil {
		return false, e
	}
	if grant["scope"] != statusRecoveryScope || grant["stop_fingerprint"] != fp || grant["build_id"] != buildinfo.BuildID || grant["source_tree_sha256"] != buildinfo.SourceTree {
		return false, nil
	}
	var proof struct {
		Review     StatusRecoveryReview `json:"review"`
		Reconciled bool                 `json:"reconciled"`
	}
	h, _ := grant["evidence_sha256"].(string)
	if e = c.preservedSource(h, &proof); e != nil {
		return false, e
	}
	return proof.Reconciled && proof.Review.StopFingerprint == fp && proof.Review.candidateOK(), nil
}

// AuthorizeStatusRecovery cannot clear an arbitrary STOP. It proves the exact
// final fixed read-only query, replays its transport prefix, preserves derived
// async facts and either consumes its remaining reply without OUT on the same
// attachment, or records disposal of the old read-only request on a proven new
// attachment. Unknown input, uncertain destructive state and identity drift deny.
func (c *Core) AuthorizeStatusRecovery(ctx context.Context, r StatusRecoveryReview) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if !r.candidateOK() || len(strings.TrimSpace(r.Note)) < 20 {
		return errors.New("explicit frozen candidate review required")
	}
	stop, e := c.Store.LatestEvent("core_safety_stop")
	if e != nil {
		return e
	}
	if stop == nil {
		return errors.New("missing STOP")
	}
	fp, e := stopFingerprint(stop)
	if e != nil {
		return e
	}
	if fp != r.StopFingerprint {
		return errors.New("stale review")
	}
	if e = c.Store.StatusRecoveryContext(r.Session, r.ReportSource, r.HostSource); e != nil {
		return e
	}
	var seed status.Report
	if e = c.preservedSource(r.ReportSource, &seed); e != nil {
		return e
	}
	pending, e := c.pendingStatusSession()
	if e != nil {
		return e
	}
	crashContinuation := stop["error"] == "interrupted status request requires explicit continuation" && pending == r.Session && seed.StopReason == ""
	if seed.Success || !seed.WriteAttempted || (!crashContinuation && (seed.StopReason == "" || seed.StopReason != stop["error"])) {
		return errors.New("STOP is not the reviewed incomplete status request")
	}
	if _, e = status.ReplayFrames(seed); e != nil {
		return fmt.Errorf("cannot classify preserved prefix: %w", e)
	}
	facts, e := c.Store.SourceFacts("session/" + r.Session + "/")
	if e != nil {
		return e
	}
	var old host.Snapshot
	if e = c.preservedSource(r.HostSource, &old); e != nil {
		return e
	}
	if e = discovery.Validate(old.Device); e != nil {
		return e
	}
	totals, e := c.Store.Summary()
	if e != nil {
		return e
	}
	if totals["unfinished_delete_attempts"] != 0 || totals["pending_temp_files"] != 0 {
		return errors.New("unresolved archive/deletion")
	}
	ds, e := c.deps.snapshot()
	if e != nil {
		return e
	}
	fresh, e := discovery.Single(ds)
	if e != nil {
		return e
	}
	a, b := old.Device.Profile, fresh.Profile
	a.Address = 0
	b.Address = 0
	if old.Device.Location != fresh.Location || !reflect.DeepEqual(a, b) {
		return errors.New("physical identity mismatch")
	}
	for _, f := range fresh.Interfaces {
		if f.Number != 4 && f.Number != 5 && f.Owner != "" {
			return errors.New("interface occupied")
		}
	}
	before, e := c.deps.capture(ctx, fresh)
	if e != nil {
		return e
	}
	if !before.HTTPS || !sipEnabled(before.SIP) {
		return errors.New("host safety proof missing")
	}
	_, prefs := c.Current()
	prefs.AutoPurge = false
	if e = c.SetPreferences(prefs); e != nil {
		return e
	}
	// Derive historical SMS only through the source-bound deterministic pipeline.
	found := false
	for _, f := range facts {
		if !strings.HasSuffix(f.Name, "/status_query") {
			continue
		}
		var q status.Report
		if e = archive.Decode(f.Data, &q); e != nil {
			return e
		}
		if q.RequestID != seed.RequestID {
			continue
		}
		if e = c.stageStatus(r.Session, f, q); e != nil {
			return e
		}
		if f.Hash == r.ReportSource {
			found = true
		}
		if found && len(q.Reads) >= len(seed.Reads) {
			if q.Command != seed.Command || q.PlannedOutHex != seed.PlannedOutHex {
				return errors.New("continuation changed request identity")
			}
			// Any later continuation must have an identical original byte prefix.
			for i, oldRead := range seed.Reads {
				if i >= len(q.Reads) || !reflect.DeepEqual(oldRead, q.Reads[i]) {
					return errors.New("continuation changed preserved reads")
				}
			}
			seed = q
		}
	}
	if !found {
		return errors.New("report not in verified source session")
	}
	if e = c.Store.Event("status_recovery_started", "", archive.M{"stop_fingerprint": fp, "session": r.Session, "build_id": r.BuildID, "old_registry_id": old.Device.RegistryID, "new_registry_id": fresh.RegistryID}); e != nil {
		return e
	}
	recoverySession := &session{core: c, device: fresh, id: r.Session, number: len(facts), inventory: map[int]record{}}
	mode := "proven_new_attachment_disposes_read_only_request"
	if fresh.RegistryID == old.Device.RegistryID {
		if e = discovery.Reconcile(old.Device, fresh); e != nil {
			return e
		}
		mode = "same_attachment_read_only_continuation"
		if !seed.Success {
			tr := c.deps.status(fresh)
			continuation, ok := tr.(interface {
				ContinueRead(context.Context, status.Report, func(status.Report) error) (status.PlanReport, error)
			})
			if !ok {
				return errors.New("status transport lacks audited continuation")
			}
			if e = tr.Connect(ctx); e != nil {
				return e
			}
			result, operationErr := continuation.ContinueRead(ctx, seed, recoverySession.saveStatus)
			if e = recoverySession.Save("status_continuation_closed", result); e != nil {
				return errors.Join(operationErr, e)
			}
			if operationErr != nil || !result.CloseSucceeded || result.EngineeringOutcome != "PASS" {
				return errors.Join(operationErr, errors.New("incomplete status continuation"))
			}
		}
	}
	ds, e = c.deps.snapshot()
	if e != nil {
		return e
	}
	last, e := discovery.Single(ds)
	if e != nil {
		return e
	}
	after, e := c.deps.capture(ctx, last)
	if e != nil {
		return e
	}
	if e = host.Compare(before, after); e != nil {
		return e
	}
	if e = c.settleStatusHandoffs(); e != nil {
		return e
	}
	if e = c.Store.Event("status_session_closed", "", archive.M{"session": r.Session, "closed": true, "complete": true, "recovery": mode}); e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	proof, e := archive.Canonical(archive.M{"review": r, "mode": mode, "old_host": old, "before": before, "after": after, "reconciled": true})
	if e != nil {
		return e
	}
	hash, e := c.Store.SaveSource("status-recovery/"+fp, proof)
	if e != nil {
		return e
	}
	return c.Store.Event("status_recovery_authorized", "", archive.M{"scope": statusRecoveryScope, "stop_fingerprint": fp, "build_id": r.BuildID, "source_tree_sha256": r.SourceTree, "evidence_sha256": hash})
}
