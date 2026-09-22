package runtime

import (
	"context"
	"errors"
	"fmt"
	"github.com/iniwex5/vohive/product/djisms-core/shutdownproof"
	"path/filepath"
	"strings"

	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"github.com/iniwex5/vohive/product/djisms-core/discovery"
	"github.com/iniwex5/vohive/product/djisms-core/host"
	"github.com/iniwex5/vohive/product/djisms-core/receive"
)

const reviewedAbort = "read fault: 0xe00002eb: IOKit read error: 0xe00002eb"

// RecoveryReview is an explicit engineering review, not a consumer retry API.
// It binds approval to one exact failure and its preserved receive-session end.
// The engineering command is not bundled into the app.
type RecoveryReview struct {
	StopFingerprint string `json:"stop_fingerprint"`
	HostSource      string `json:"host_source_sha256"`
	EndSource       string `json:"end_source_sha256"`
	Note            string `json:"operator_review"`
}

func stopFingerprint(stop archive.M) (string, error) {
	b, e := archive.Canonical(stop)
	if e != nil {
		return "", e
	}
	return archive.Hash(b), nil
}

func (c *Core) reviewedStop(stop archive.M) (bool, error) {
	if yes, e := shutdownproof.Allowed(c.Store); yes || e != nil {
		return yes, e
	}
	if yes, e := c.reviewedStatusStop(stop); yes || e != nil {
		return yes, e
	}
	if stop["error"] == reviewedDNS {
		return c.reviewedDNSStop(stop)
	}
	if stop["error"] != reviewedAbort && stop["error"] != receive.LegacyRemovalError {
		return false, nil
	}
	review, e := c.Store.LatestEvent("receive_recovery_authorized")
	if e != nil || review == nil {
		return false, e
	}
	fingerprint, e := stopFingerprint(stop)
	if e != nil {
		return false, e
	}
	return review["stop_fingerprint"] == fingerprint && review["scope"] == "reviewed_receive_abort" && review["receive_only_start"] == true, nil
}

func (c *Core) preservedSource(hash string, target any) error {
	if len(hash) != 64 || strings.Trim(hash, "0123456789abcdef") != "" {
		return errors.New("invalid evidence hash")
	}
	b, e := archive.Read(filepath.Join(c.Store.Root, "sources", hash+".bin"))
	if e != nil {
		return e
	}
	if archive.Hash(b) != hash {
		return errors.New("evidence hash mismatch")
	}
	return archive.Decode(b, target)
}

// AuthorizeReceiveRecovery requires exclusive archive ownership. It does not
// open USB interfaces or issue any AT command. Historical failure records stay
// immutable. Startup still performs all normal device/settings reconciliation.
func (c *Core) AuthorizeReceiveRecovery(ctx context.Context, r RecoveryReview) error {
	stop, e := c.Store.LatestEvent("core_safety_stop")
	if e != nil {
		return e
	}
	if stop == nil || (stop["error"] != reviewedAbort && stop["error"] != receive.LegacyRemovalError) {
		return errors.New("failure is not the reviewed receive-only USB abort")
	}
	fingerprint, e := stopFingerprint(stop)
	if e != nil {
		return e
	}
	if fingerprint != r.StopFingerprint || len(r.Note) < 20 {
		return errors.New("missing or stale explicit review")
	}
	totals, e := c.Store.Summary()
	if e != nil {
		return e
	}
	if totals["unfinished_delete_attempts"] != 0 || totals["pending_temp_files"] != 0 {
		return errors.New("unresolved archive or deletion; recovery denied")
	}
	latest, e := c.Store.LatestEvent("source_preserved")
	if e != nil {
		return e
	}
	name, _ := latest["name"].(string)
	if latest["sha256"] != r.EndSource || !strings.HasSuffix(name, "/session_end") {
		return errors.New("review does not match latest closed session")
	}
	var end receive.Summary
	var endFields archive.M
	if e = c.preservedSource(r.EndSource, &end); e != nil {
		return e
	}
	if e = c.preservedSource(r.EndSource, &endFields); e != nil {
		return e
	}
	if _, ok := endFields["read_indices"]; !ok {
		return errors.New("not a receive-session summary")
	}
	if _, ok := endFields["delete_attempted"]; ok {
		return errors.New("purge failure cannot use receive recovery")
	}
	if stop["error"] == receive.LegacyRemovalError {
		// All old native references were unconditionally released by the reviewed
		// pre-fix close implementation. Exclusive archive ownership plus fresh
		// identity/IF2 checks below prevent overlap with the old receive process.
		parts := strings.Split(name, "/")
		if len(parts) != 4 {
			return errors.New("invalid legacy session name")
		}
		facts, e := c.Store.PreservedSession(parts[1])
		if e != nil {
			return e
		}
		events := make([]receive.ReviewEvent, 0, len(facts))
		for _, f := range facts {
			events = append(events, receive.ReviewEvent{Kind: f.Kind, Data: f.Data})
		}
		if e = receive.ReviewLegacyEmptyRemoval(events); e != nil {
			return fmt.Errorf("legacy removal proof: %w", e)
		}
	} else if !end.Closed || end.Error != reviewedAbort {
		return errors.New("session not closed with reviewed abort")
	}
	var old host.Snapshot
	if e = c.preservedSource(r.HostSource, &old); e != nil {
		return e
	}
	if e = discovery.Validate(old.Device); e != nil {
		return e
	}
	ds, e := c.deps.snapshot()
	if e != nil {
		return e
	}
	fresh, e := discovery.Single(ds)
	if e != nil {
		return e
	}
	if fresh.RegistryID == old.Device.RegistryID || fresh.Interfaces[2].Owner != "" {
		return errors.New("physical reconnect with unowned IF2 required")
	}
	before, e := c.deps.capture(ctx, fresh)
	if e != nil {
		return e
	}
	afterDevices, e := c.deps.snapshot()
	if e != nil {
		return e
	}
	afterDevice, e := discovery.Single(afterDevices)
	if e != nil {
		return e
	}
	after, e := c.deps.capture(ctx, afterDevice)
	if e != nil {
		return e
	}
	if e = host.Compare(before, after); e != nil {
		return e
	}
	evidence, e := archive.Canonical(archive.M{"review": r, "old_host": old, "session_end": endFields, "new_host_before": before, "new_host_after": after})
	if e != nil {
		return e
	}
	proof, e := c.Store.SaveSource("recovery/"+fingerprint, evidence)
	if e != nil {
		return e
	}
	_, prefs := c.Current()
	prefs.AutoPurge = false
	if e = c.SetPreferences(prefs); e != nil {
		return e
	}
	if e = c.Store.Event("receive_recovery_authorized", "", archive.M{"scope": "reviewed_receive_abort", "stop_fingerprint": fingerprint, "evidence_sha256": proof, "receive_only_start": true, "old_registry_id": old.Device.RegistryID, "new_registry_id": fresh.RegistryID}); e != nil {
		return fmt.Errorf("record recovery: %w", e)
	}
	return nil
}
