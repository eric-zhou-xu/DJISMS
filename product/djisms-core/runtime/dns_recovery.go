package runtime

import (
	"context"
	"errors"
	"reflect"
	"strings"

	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"github.com/iniwex5/vohive/product/djisms-core/buildinfo"
	"github.com/iniwex5/vohive/product/djisms-core/discovery"
	"github.com/iniwex5/vohive/product/djisms-core/host"
	"github.com/iniwex5/vohive/product/djisms-core/receive"
)

const reviewedDNS = `ECM HTTPS verification: Get "https://www.apple.com/library/test/success.html": dial tcp4: lookup www.apple.com: no such host`
const dnsScope = "closed_empty_receive_dns_v1"

type DNSRecoveryReview struct {
	StopFingerprint string `json:"stop_fingerprint"`
	Session         string `json:"session"`
	EndSource       string `json:"end_source_sha256"`
	HostSource      string `json:"host_source_sha256"`
	BuildID         string `json:"build_id"`
	SourceTree      string `json:"source_tree_sha256"`
	Note            string `json:"operator_review"`
}

func sipEnabled(s string) bool {
	return strings.Contains(s, "enabled") && !strings.Contains(s, "disabled")
}

func validCandidate(r DNSRecoveryReview) bool {
	return r.BuildID == buildinfo.BuildID && r.SourceTree == buildinfo.SourceTree && r.BuildID != "unversioned-development" && len(r.SourceTree) == 64 && strings.Trim(r.SourceTree, "0123456789abcdef") == ""
}

func (c *Core) reviewedDNSStop(stop archive.M) (bool, error) {
	if stop["error"] != reviewedDNS {
		return false, nil
	}
	grant, e := c.Store.LatestEvent("dns_receive_recovery_authorized")
	if e != nil || grant == nil {
		return false, e
	}
	fingerprint, e := stopFingerprint(stop)
	if e != nil {
		return false, e
	}
	if grant["scope"] != dnsScope || grant["stop_fingerprint"] != fingerprint || grant["receive_only_start"] != true || grant["build_id"] != buildinfo.BuildID || grant["source_tree_sha256"] != buildinfo.SourceTree {
		return false, nil
	}
	h, _ := grant["evidence_sha256"].(string)
	var proof struct {
		Review DNSRecoveryReview `json:"review"`
	}
	if e = c.preservedSource(h, &proof); e != nil {
		return false, e
	}
	return validCandidate(proof.Review) && proof.Review.StopFingerprint == fingerprint, nil
}

// AuthorizeDNSRecovery is an engineering-only, explicit grant. It never starts
// Core, opens USB, changes DNS/TLS policy or deletes data. History is append-only.
func (c *Core) AuthorizeDNSRecovery(ctx context.Context, r DNSRecoveryReview) error {
	if !validCandidate(r) || len(strings.TrimSpace(r.Note)) < 20 {
		return errors.New("explicit frozen candidate review required")
	}
	stop, e := c.Store.LatestEvent("core_safety_stop")
	if e != nil {
		return e
	}
	if stop == nil || stop["error"] != reviewedDNS {
		return errors.New("not the exact reviewed post-receive DNS failure")
	}
	fingerprint, e := stopFingerprint(stop)
	if e != nil {
		return e
	}
	if fingerprint != r.StopFingerprint {
		return errors.New("stale STOP review")
	}
	if e = c.Store.ClosedReceiveContext(r.Session, r.EndSource, r.HostSource); e != nil {
		return e
	}
	totals, e := c.Store.Summary()
	if e != nil {
		return e
	}
	if totals["unfinished_delete_attempts"] != 0 || totals["pending_temp_files"] != 0 {
		return errors.New("unresolved archive/deletion")
	}
	facts, e := c.Store.PreservedSession(r.Session)
	if e != nil {
		return e
	}
	events := make([]receive.ReviewEvent, 0, len(facts))
	for _, f := range facts {
		events = append(events, receive.ReviewEvent{Kind: f.Kind, Data: f.Data})
	}
	if e = receive.ReviewClosedEmpty(events); e != nil {
		return e
	}
	var old host.Snapshot
	if e = c.preservedSource(r.HostSource, &old); e != nil {
		return e
	}
	if e = discovery.Validate(old.Device); e != nil {
		return e
	}
	if !old.HTTPS || !sipEnabled(old.SIP) {
		return errors.New("historical host not verified")
	}
	ds, e := c.deps.snapshot()
	if e != nil {
		return e
	}
	fresh, e := discovery.Single(ds)
	if e != nil {
		return e
	}
	// Registry IDs and USB address are attachment epochs, never rewritten in
	// evidence. The frozen descriptor profile and physical location must match.
	a, b := old.Device.Profile, fresh.Profile
	a.Address = 0
	b.Address = 0
	if old.Device.Location != fresh.Location || !reflect.DeepEqual(a, b) {
		return errors.New("historical physical identity differs")
	}
	for _, f := range fresh.Interfaces {
		if f.Number != 4 && f.Number != 5 && f.Owner != "" {
			return errors.New("non-ECM interface occupied")
		}
	}
	before, e := c.deps.capture(ctx, fresh)
	if e != nil {
		return e
	}
	ds, e = c.deps.snapshot()
	if e != nil {
		return e
	}
	second, e := discovery.Single(ds)
	if e != nil {
		return e
	}
	if e = discovery.Reconcile(fresh, second); e != nil {
		return e
	}
	after, e := c.deps.capture(ctx, second)
	if e != nil {
		return e
	}
	if !sipEnabled(before.SIP) || !sipEnabled(after.SIP) {
		return errors.New("SIP not enabled")
	}
	if e = host.Compare(before, after); e != nil {
		return e
	}
	if e = discovery.Reconcile(fresh, before.Device); e != nil {
		return e
	}
	ds, e = c.deps.snapshot()
	if e != nil {
		return e
	}
	last, e := discovery.Single(ds)
	if e != nil {
		return e
	}
	if e = discovery.Reconcile(second, last); e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	evidence, e := archive.Canonical(archive.M{"scope": dnsScope, "review": r, "old_host": old, "new_host_before": before, "new_host_after": after, "replayed_facts": len(facts), "archive_summary": totals})
	if e != nil {
		return e
	}
	proof, e := c.Store.SaveSource("dns_recovery/"+fingerprint, evidence)
	if e != nil {
		return e
	}
	_, prefs := c.Current()
	prefs.AutoPurge = false
	if e = c.SetPreferences(prefs); e != nil {
		return e
	}
	return c.Store.Event("dns_receive_recovery_authorized", "", archive.M{"scope": dnsScope, "stop_fingerprint": fingerprint, "evidence_sha256": proof, "build_id": r.BuildID, "source_tree_sha256": r.SourceTree, "receive_only_start": true})
}
