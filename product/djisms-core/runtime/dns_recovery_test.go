package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"github.com/iniwex5/vohive/product/djisms-core/buildinfo"
	"github.com/iniwex5/vohive/product/djisms-core/discovery"
	"github.com/iniwex5/vohive/product/djisms-core/host"
)

func dnsFixture(t *testing.T, mode string) (*Core, DNSRecoveryReview, discovery.Device) {
	t.Helper()
	oldBuild, oldSource := buildinfo.BuildID, buildinfo.SourceTree
	buildinfo.BuildID = "offline-dns-candidate"
	buildinfo.SourceTree = strings.Repeat("a", 64)
	t.Cleanup(func() { buildinfo.BuildID = oldBuild; buildinfo.SourceTree = oldSource })
	c, d := testCore(t)
	if mode == "unknown_delete" {
		rid, e := c.Store.Ingest("offline-dns-fixture", "0000", archive.M{"device_key": "audited-device", "connection_id": "session-test", "received_at": "2026-09-18T00:00:00Z", "storage": "ME", "index": 0}, archive.M{"state": "binary"})
		if e != nil {
			t.Fatal(e)
		}
		_, h := archive.PDUHashes("0000")
		if e = c.Store.Event("delete_intent", rid, archive.M{"attempt_id": "fixture", "storage": "ME", "index": 0, "pdu_sha256": h, "connection_id": "session-test", "mode": "simulation"}); e != nil {
			t.Fatal(e)
		}
	}
	b, _ := archive.Canonical(host.Snapshot{Device: d, HTTPS: true, SIP: "enabled"})
	h, e := c.Store.SaveSource("session/previous/000001/host_after", b)
	if e != nil {
		t.Fatal(e)
	}
	id := strings.Repeat("d", 32)
	b, e = os.ReadFile("../receive/testdata/closed-empty-session.json")
	if e != nil {
		t.Fatal(e)
	}
	var facts []struct {
		Kind string
		Data json.RawMessage
	}
	if e = json.Unmarshal(b, &facts); e != nil {
		t.Fatal(e)
	}
	end := ""
	for i, f := range facts {
		if mode == "missing_fact" && i == 10 {
			continue
		}
		if mode == "wrong_end" && f.Kind == "session_end" {
			f.Data = []byte(`{"closed":true}`)
		}
		if mode == "bad_raw" && i == 2 {
			f.Data = []byte(`{"valid_hex":"00","diagnostic":{}}`)
		}
		if mode == "unknown_fact" && i == 2 {
			f.Kind = "delete_result"
		}
		if mode == "interleave" && i == 10 {
			if e = c.Store.Event("notification", "", archive.M{}); e != nil {
				t.Fatal(e)
			}
		}
		end, e = c.Store.SaveSource(fmt.Sprintf("session/%s/%06d/%s", id, i+1, f.Kind), f.Data)
		if e != nil {
			t.Fatal(e)
		}
	}
	if mode == "not_adjacent" {
		c.Store.Event("unrelated", "", archive.M{})
	}
	c.stop(errors.New(reviewedDNS))
	stop, _ := c.Store.LatestEvent("core_safety_stop")
	fp, _ := stopFingerprint(stop)
	r := DNSRecoveryReview{StopFingerprint: fp, Session: id, HostSource: h, EndSource: end, BuildID: buildinfo.BuildID, SourceTree: buildinfo.SourceTree, Note: "Explicit review of preserved closed empty receive session and DNS failure."}
	fresh := d
	fresh.RegistryID++
	c.deps.snapshot = func() ([]discovery.Device, error) { return []discovery.Device{fresh}, nil }
	c.deps.capture = func(_ context.Context, d discovery.Device) (host.Snapshot, error) {
		return host.Snapshot{Device: d, HTTPS: true, SIP: "enabled"}, nil
	}
	return c, r, fresh
}

func TestDNSRecoveryHistoricalRawReplayAndDurability(t *testing.T) {
	c, r, _ := dnsFixture(t, "")
	if e := c.AuthorizeDNSRecovery(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	stop, _ := c.Store.LatestEvent("core_safety_stop")
	fp, _ := stopFingerprint(stop)
	if fp != r.StopFingerprint {
		t.Fatal("STOP mutation")
	}
	if ok, e := c.reviewedStop(stop); e != nil || !ok {
		t.Fatal(ok, e)
	}
	_, p := c.Current()
	if p.AutoPurge {
		t.Fatal("purge enabled")
	}
	root := c.Store.Root
	if e := c.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := New(root, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	if ok, e := reopened.reviewedStop(stop); e != nil || !ok {
		t.Fatal(ok, e)
	}
	buildinfo.BuildID += "-different"
	if ok, _ := reopened.reviewedStop(stop); ok {
		t.Fatal("cross-build grant")
	}
	buildinfo.BuildID = r.BuildID
	reopened.stop(errors.New(reviewedDNS))
	latest, _ := reopened.Store.LatestEvent("core_safety_stop")
	if ok, _ := reopened.reviewedStop(latest); ok {
		t.Fatal("grant cleared next STOP")
	}
}

func TestDNSRecoveryFailClosed(t *testing.T) {
	for _, mode := range []string{"missing_fact", "wrong_end", "bad_raw", "unknown_fact", "interleave", "not_adjacent", "stale_review", "wrong_source", "wrong_host", "wrong_end_hash", "unfrozen", "different_error", "tls_failure", "no_https", "sip_disabled", "identity_changed", "multiple_devices", "occupied_if2", "snapshot_race", "cancelled", "proof_write_fault", "preference_write_fault", "grant_write_fault", "pending_archive", "unknown_delete"} {
		t.Run(mode, func(t *testing.T) {
			c, r, d := dnsFixture(t, mode)
			ctx := context.Background()
			switch mode {
			case "pending_archive":
				if e := os.WriteFile(c.Store.Root+"/raw/.pending-incomplete", []byte(`{"schema":`), 0600); e != nil {
					t.Fatal(e)
				}
			case "stale_review":
				r.StopFingerprint = "stale"
			case "wrong_source":
				r.SourceTree = strings.Repeat("b", 64)
			case "wrong_host":
				r.HostSource = r.EndSource
			case "wrong_end_hash":
				r.EndSource = r.HostSource
			case "unfrozen":
				buildinfo.SourceTree = "unfrozen"
				r.SourceTree = "unfrozen"
			case "different_error":
				c.stop(errors.New("TLS certificate verification failed"))
			case "tls_failure":
				c.deps.capture = func(context.Context, discovery.Device) (host.Snapshot, error) {
					return host.Snapshot{}, errors.New("certificate invalid")
				}
			case "no_https", "sip_disabled":
				c.deps.capture = func(_ context.Context, d discovery.Device) (host.Snapshot, error) {
					return host.Snapshot{Device: d, HTTPS: mode != "no_https", SIP: "disabled"}, nil
				}
			case "identity_changed":
				d.Location++
				c.deps.snapshot = func() ([]discovery.Device, error) { return []discovery.Device{d}, nil }
			case "multiple_devices":
				c.deps.snapshot = func() ([]discovery.Device, error) { return []discovery.Device{d, d}, nil }
			case "occupied_if2":
				d.Interfaces = append([]discovery.Interface(nil), d.Interfaces...)
				d.Interfaces[2].Owner = "other"
				c.deps.snapshot = func() ([]discovery.Device, error) { return []discovery.Device{d}, nil }
			case "snapshot_race":
				n := 0
				c.deps.snapshot = func() ([]discovery.Device, error) {
					n++
					x := d
					x.RegistryID += uint64(n)
					return []discovery.Device{x}, nil
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "proof_write_fault", "preference_write_fault", "grant_write_fault":
				n := 0
				c.Store.Fault = func(point string) error {
					if strings.HasPrefix(point, "journal_durable:") {
						n++
						want := map[string]int{"proof_write_fault": 1, "preference_write_fault": 2, "grant_write_fault": 3}[mode]
						if n == want {
							return errors.New("injected durability interruption")
						}
					}
					return nil
				}
			}
			if e := c.AuthorizeDNSRecovery(ctx, r); e == nil {
				t.Fatal("unsafe grant accepted")
			}
			// Fault injection can durably commit a grant before reporting interruption;
			// the journal remains authoritative and reopening must verify it.
			if strings.HasSuffix(mode, "write_fault") {
				root := c.Store.Root
				if e := c.Close(); e != nil {
					t.Fatal(e)
				}
				reopened, e := New(root, nil)
				if e != nil {
					t.Fatal(e)
				}
				defer reopened.Close()
				stop, _ := reopened.Store.LatestEvent("core_safety_stop")
				ok, e := reopened.reviewedStop(stop)
				if e != nil || ok != (mode == "grant_write_fault") {
					t.Fatal("incorrect durable authorization", ok, e)
				}
			} else {
				ev, e := c.Store.LatestEvent("dns_receive_recovery_authorized")
				if e != nil || ev != nil {
					t.Fatal("unexpected grant", e)
				}
			}
		})
	}
}
