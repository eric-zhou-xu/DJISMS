package runtime

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"github.com/iniwex5/vohive/product/djisms-core/discovery"
	"github.com/iniwex5/vohive/product/djisms-core/host"
	"github.com/iniwex5/vohive/product/djisms-core/receive"
)

func recoveryFixture(t *testing.T) (*Core, RecoveryReview, discovery.Device) {
	t.Helper()
	c, old := testCore(t)
	savedHost := host.Snapshot{Device: old, HTTPS: true, SIP: "enabled"}
	b, _ := archive.Canonical(savedHost)
	h, e := c.Store.SaveSource("session/fixture/host_after", b)
	if e != nil {
		t.Fatal(e)
	}
	end := receive.Summary{Closed: true, Error: reviewedAbort, ReadIndices: []int{}}
	b, _ = archive.Canonical(end)
	last, e := c.Store.SaveSource("session/fixture/session_end", b)
	if e != nil {
		t.Fatal(e)
	}
	c.stop(errors.New(reviewedAbort))
	stop, _ := c.Store.LatestEvent("core_safety_stop")
	fingerprint, _ := stopFingerprint(stop)
	fresh := old
	fresh.RegistryID++
	c.deps.snapshot = func() ([]discovery.Device, error) { return []discovery.Device{fresh}, nil }
	c.deps.capture = func(_ context.Context, d discovery.Device) (host.Snapshot, error) {
		return host.Snapshot{Device: d, HTTPS: true, SIP: "enabled"}, nil
	}
	return c, RecoveryReview{fingerprint, h, last, "Operator inspected archived receive-only closure and physical reconnect."}, fresh
}

func TestReviewedReceiveRecoveryPreservesStopAndCannotApproveNextFailure(t *testing.T) {
	c, r, _ := recoveryFixture(t)
	if e := c.AuthorizeReceiveRecovery(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	stop, e := c.Store.LatestEvent("core_safety_stop")
	if e != nil {
		t.Fatal(e)
	}
	fingerprint, _ := stopFingerprint(stop)
	if fingerprint != r.StopFingerprint {
		t.Fatal("original stop changed")
	}
	if yes, e := c.reviewedStop(stop); e != nil || !yes {
		t.Fatal("review missing", e)
	}
	_, prefs := c.Current()
	if prefs.AutoPurge {
		t.Fatal("recovery may not start with automatic purge")
	}
	// This also exercises the durable reopen path and journal/projection checks.
	root := c.Store.Root
	if e = c.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := New(root, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	if yes, e := reopened.reviewedStop(stop); e != nil || !yes {
		t.Fatal("review lost at restart", e)
	}
	reopened.stop(errors.New(reviewedAbort))
	newer, _ := reopened.Store.LatestEvent("core_safety_stop")
	if yes, e := reopened.reviewedStop(newer); e != nil || yes {
		t.Fatal("old review authorized another failure", e)
	}
}

func TestReceiveRecoveryRejectsUnreviewedOrUnsafeState(t *testing.T) {
	for _, scenario := range []string{"stale_review", "same_device", "multiple_devices", "occupied_if2", "network_failure", "critical_error", "wrong_session"} {
		t.Run(scenario, func(t *testing.T) {
			c, r, d := recoveryFixture(t)
			switch scenario {
			case "stale_review":
				r.StopFingerprint = "wrong"
			case "same_device":
				d.RegistryID--
				c.deps.snapshot = func() ([]discovery.Device, error) { return []discovery.Device{d}, nil }
			case "multiple_devices":
				c.deps.snapshot = func() ([]discovery.Device, error) { return []discovery.Device{d, d}, nil }
			case "occupied_if2":
				d.Interfaces[2].Owner = "another process"
				c.deps.snapshot = func() ([]discovery.Device, error) { return []discovery.Device{d}, nil }
			case "network_failure":
				c.deps.capture = func(context.Context, discovery.Device) (host.Snapshot, error) {
					return host.Snapshot{}, errors.New("HTTPS failed")
				}
			case "critical_error":
				c.stop(errors.New("purge result unknown"))
			case "wrong_session":
				r.EndSource = r.HostSource
			case "unknown_delete":
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				setup(c, d, smsFixture(t), cancel, true)
				if e := c.connected(ctx, d); e == nil {
					t.Fatal("fixture did not leave unknown deletion")
				}
			}
			if e := c.AuthorizeReceiveRecovery(context.Background(), r); e == nil {
				t.Fatal("unsafe recovery authorized")
			}
			if event, e := c.Store.LatestEvent("receive_recovery_authorized"); e != nil || event != nil {
				t.Fatal("unexpected recovery record", e)
			}
		})
	}
}

func TestReviewedLegacyRemovalNeedsReplayAndNewIdentity(t *testing.T) {
	for _, mode := range []string{"complete", "same_device", "missing_fact", "active_notice", "wrong_end"} {
		t.Run(mode, func(t *testing.T) {
			c, r, fresh := recoveryFixture(t)
			id := strings.Repeat("b", 32)
			n := 0
			save := func(kind string, v any) string {
				n++
				b, e := archive.Canonical(v)
				if e != nil {
					t.Fatal(e)
				}
				h, e := c.Store.SaveSource(fmt.Sprintf("session/%s/%06d/%s", id, n, kind), b)
				if e != nil {
					t.Fatal(e)
				}
				return h
			}
			wires := []string{"AT+CMGF?", "AT+CPMS?", "AT+CNMI?", "AT+CSMS?", "AT+CMGL=4", "AT+CPMS?"}
			bodies := []string{"+CMGF: 0", `+CPMS: "ME",0,23,"ME",0,23,"ME",0,23`, "+CNMI: 2,1,0,0,0", "+CSMS: 0,1,1,1", "", `+CPMS: "ME",0,23,"ME",0,23,"ME",0,23`}
			for i, wire := range wires {
				save("out_intent", archive.M{"command": wire})
				save("out_succeeded", receive.Command{Kind: uint8(i + 1)})
				raw := []byte(wire + "\r\r\n" + bodies[i] + "\r\nOK\r\n")
				size := uint32(len(raw))
				save("usb_read", archive.M{"valid_hex": hex.EncodeToString(raw), "diagnostic": receive.ReadDiagnostic{CountValid: true, RawSize: size, ActualBytes: &size}})
				if mode == "missing_fact" && i == 3 {
					n++
				} else {
					save("response", archive.M{})
				}
				if i == 4 {
					save("snapshot_raw", []any{})
				}
			}
			if mode == "active_notice" {
				raw := []byte("+CMTI: \"ME\",0\r\n")
				size := uint32(len(raw))
				save("usb_read", archive.M{"valid_hex": hex.EncodeToString(raw), "diagnostic": receive.ReadDiagnostic{CountValid: true, RawSize: size, ActualBytes: &size}})
			}
			save("usb_read", archive.M{"valid_hex": "", "diagnostic": receive.ReadDiagnostic{ReturnCode: 0xe00002ed}})
			end := receive.Summary{Error: receive.LegacyRemovalError}
			if mode == "wrong_end" {
				end.Error = "other"
			}
			r.EndSource = save("session_end", end)
			c.stop(errors.New(receive.LegacyRemovalError))
			stop, _ := c.Store.LatestEvent("core_safety_stop")
			r.StopFingerprint, _ = stopFingerprint(stop)
			if mode == "same_device" {
				fresh.RegistryID--
				c.deps.snapshot = func() ([]discovery.Device, error) { return []discovery.Device{fresh}, nil }
			}

			e := c.AuthorizeReceiveRecovery(context.Background(), r)
			if mode == "complete" {
				if e != nil {
					t.Fatal(e)
				}
				ok, e := c.reviewedStop(stop)
				if e != nil || !ok {
					t.Fatal(ok, e)
				}
			} else if e == nil {
				t.Fatal("unsafe recovery accepted")
			}
		})
	}
}
