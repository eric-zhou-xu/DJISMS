package runtime

import (
	"context"
	"errors"
	"github.com/iniwex5/vohive/internal/smsreceive"
	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"github.com/iniwex5/vohive/product/djisms-core/discovery"
	"github.com/iniwex5/vohive/product/djisms-core/host"
	"github.com/iniwex5/vohive/product/djisms-core/receive"
	"github.com/iniwex5/vohive/product/djisms-core/usbrestore"
	"strings"
	"testing"
	"time"
)

func sleepFixture(t *testing.T) (*Core, discovery.Device, *int) {
	c, d := testCore(t)
	if e := c.saveConnectionRecovery(d.RegistryID, sleepIdleReason, d.Location); e != nil {
		t.Fatal(e)
	}
	c.deps.mediaInactive = func(discovery.Device) (bool, error) { return true, nil }
	calls := new(int)
	c.deps.reenumerate = func(_ discovery.Device, save func() error) (usbrestore.Result, error) {
		if e := save(); e != nil {
			return usbrestore.Result{}, e
		}
		event, e := c.sleepRecoveryIntent()
		if e != nil || event == nil {
			t.Fatal("mutation before durable intent", event, e)
		}
		*calls++
		return usbrestore.Result{Attempted: true}, nil
	}
	return c, d, calls
}
func TestSleepRecoverySingleAttemptSurvivesRestart(t *testing.T) {
	c, d, calls := sleepFixture(t)
	now := time.Now()
	ctx := context.Background()
	if e := c.trySleepRecovery(ctx, d, now); e != nil {
		t.Fatal(e)
	}
	if *calls != 0 {
		t.Fatal("no grace")
	}
	if e := c.trySleepRecovery(ctx, d, now.Add(6*time.Second)); e != nil {
		t.Fatal(e)
	}
	if *calls != 1 {
		t.Fatal(*calls)
	}
	if e := c.loadConnectionRecovery(); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		if e := c.trySleepRecovery(ctx, d, now.Add(time.Duration(8+i)*time.Second)); e != nil {
			t.Fatal(e)
		}
	}
	if *calls != 1 {
		t.Fatal("repeated after durable intent")
	}
}
func TestSleepRecoveryRequiresSpecificFault(t *testing.T) {
	for _, kind := range []string{"no_checkpoint", "not_sleep", "legacy_location", "different_registry", "different_port", "paused", "cancelled", "active_link", "busy_if0", "busy_if2"} {
		t.Run(kind, func(t *testing.T) {
			c, d, calls := sleepFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "no_checkpoint":
				c.recoveryCheckpoint = nil
			case "not_sleep":
				c.recoveryCheckpoint.Sleep = false
			case "legacy_location":
				c.recoveryCheckpoint.Location = 0
			case "different_registry":
				d.RegistryID++
			case "different_port":
				c.recoveryCheckpoint.Location++
			case "paused":
				c.powerPaused = true
			case "cancelled":
				cancel()
			case "active_link":
				c.deps.mediaInactive = func(discovery.Device) (bool, error) { return false, nil }
			case "busy_if0":
				d.Interfaces[0].Owner = "another process"
			case "busy_if2":
				d.Interfaces[2].Owner = "another process"
			}
			c.recoveryInactiveSince = time.Now().Add(-10 * time.Second)
			e := c.trySleepRecovery(ctx, d, time.Now())
			if strings.HasPrefix(kind, "busy_") && e == nil {
				t.Fatal("must fail closed on ownership change")
			}
			if *calls != 0 {
				t.Fatal("unexpected reenumeration")
			}
			event, err := c.sleepRecoveryIntent()
			if err != nil || event != nil {
				t.Fatal("unexpected intent", event, err)
			}
		})
	}
}
func TestSleepRecoveryInactiveMustBeContinuous(t *testing.T) {
	c, d, calls := sleepFixture(t)
	now := time.Now()
	ctx := context.Background()
	_ = c.trySleepRecovery(ctx, d, now)
	c.deps.mediaInactive = func(discovery.Device) (bool, error) { return false, nil }
	_ = c.trySleepRecovery(ctx, d, now.Add(6*time.Second))
	c.deps.mediaInactive = func(discovery.Device) (bool, error) { return true, nil }
	_ = c.trySleepRecovery(ctx, d, now.Add(7*time.Second))
	if *calls != 0 {
		t.Fatal("grace did not reset")
	}
}
func TestSleepRecoveryEvidenceFailurePreventsMutation(t *testing.T) {
	c, d, calls := sleepFixture(t)
	c.recoveryInactiveSince = time.Now().Add(-10 * time.Second)
	c.Store.Fault = func(point string) error {
		if point == "journal_durable:sleep_usb_reenumeration_intent" {
			return errors.New("injected persistence failure")
		}
		return nil
	}
	if e := c.trySleepRecovery(context.Background(), d, time.Now()); e == nil {
		t.Fatal("expected persistence error")
	}
	if *calls != 0 {
		t.Fatal("mutation after evidence failure")
	}
}
func TestSleepRecoveryUnknownResultCannotRetry(t *testing.T) {
	c, d, calls := sleepFixture(t)
	c.recoveryInactiveSince = time.Now().Add(-10 * time.Second)
	c.deps.reenumerate = func(_ discovery.Device, save func() error) (usbrestore.Result, error) {
		if e := save(); e != nil {
			return usbrestore.Result{}, e
		}
		*calls++
		return usbrestore.Result{Attempted: true, ReenumerateCode: 1}, errors.New("unknown API outcome")
	}
	if e := c.trySleepRecovery(context.Background(), d, time.Now()); e == nil {
		t.Fatal("unknown result accepted")
	}
	if e := c.loadConnectionRecovery(); e != nil {
		t.Fatal(e)
	}
	if e := c.trySleepRecovery(context.Background(), d, time.Now()); e != nil {
		t.Fatal(e)
	}
	if *calls != 1 {
		t.Fatal("unknown retried")
	}
}
func TestSleepRecoveryCrashIntentAndDeadline(t *testing.T) {
	c, d, calls := sleepFixture(t)
	if e := c.Store.Event("sleep_usb_reenumeration_intent", "", archive.M{"checkpoint": c.recoveryID, "deadline": time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)}); e != nil {
		t.Fatal(e)
	}
	if e := c.loadConnectionRecovery(); e != nil {
		t.Fatal(e)
	}
	if e := c.trySleepRecovery(context.Background(), d, time.Now()); e != nil {
		t.Fatal(e)
	}
	if *calls != 0 {
		t.Fatal("crash retried")
	}
	if e := c.sleepRecoveryDeadline(); e == nil {
		t.Fatal("deadline ignored")
	}
	c.recoveryID = ""
	if e := c.sleepRecoveryDeadline(); e != nil {
		t.Fatal("resolved checkpoint still blocks", e)
	}
}

func TestSleepRecoveryLinkHealsBeforeMutation(t *testing.T) {
	c, d, calls := sleepFixture(t)
	c.recoveryInactiveSince = time.Now().Add(-10 * time.Second)
	observations := 0
	c.deps.mediaInactive = func(discovery.Device) (bool, error) { observations++; return observations == 1, nil }
	if e := c.trySleepRecovery(context.Background(), d, time.Now()); e != nil {
		t.Fatal(e)
	}
	if *calls != 0 {
		t.Fatal("healed link was reset")
	}
	if e, err := c.sleepRecoveryIntent(); err != nil || e != nil {
		t.Fatal("intent consumed for healed link", e, err)
	}
}
func TestSleepRecoveryRunReconcilesAfterEnumeration(t *testing.T) {
	c, d, calls := sleepFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	setup(c, d, smsFixture(t), cancel, false)
	c.prefs.AutoPurge = false
	c.recoveryInactiveSince = time.Now().Add(-10 * time.Second)
	restored := false
	churn := false
	capture := c.deps.capture
	c.deps.capture = func(ctx context.Context, x discovery.Device) (host.Snapshot, error) {
		if !restored {
			return host.Snapshot{}, host.NotReady{Err: errors.New("no IPv4")}
		}
		return capture(ctx, x)
	}
	c.deps.snapshot = func() ([]discovery.Device, error) {
		if restored && !churn {
			churn = true
			return nil, errors.New("device tree changed during own reenumeration")
		}
		return []discovery.Device{d}, nil
	}
	c.deps.reenumerate = func(_ discovery.Device, save func() error) (usbrestore.Result, error) {
		if e := save(); e != nil {
			return usbrestore.Result{}, e
		}
		*calls++
		restored = true
		d.RegistryID++
		return usbrestore.Result{Attempted: true}, nil
	}
	c.deps.receive = func(discovery.Device) receiveTransport {
		return receiveFunc(func(_ context.Context, _ receive.Config, s receive.Sink) (receive.Summary, error) {
			if e := s.Snapshot([]smsreceive.RawMessage{}); e != nil {
				return receive.Summary{}, e
			}
			if e := s.Ready(0); e != nil {
				return receive.Summary{}, e
			}
			cancel()
			return receive.Summary{Closed: true, Reconciled: true}, nil
		})
	}
	c.run(ctx)
	if *calls != 1 || !churn {
		t.Fatal("missing bounded recovery", *calls, churn)
	}
	if c.recoveryID != "" {
		t.Fatal("not reconciled")
	}
	if stop, e := c.Store.LatestEvent("core_safety_stop"); e != nil || stop != nil {
		t.Fatal("unexpected stop", stop, e)
	}
	if r, e := c.Store.LatestEvent("receive_connection_restored"); e != nil || r == nil || r["inventory_verified"] != true {
		t.Fatal("missing inventory verification", r, e)
	}
}

func TestSleepRecoveryThreeIndependentCycles(t *testing.T) {
	c, d, calls := sleepFixture(t)
	for cycle := 0; cycle < 3; cycle++ {
		if cycle > 0 {
			if err := c.saveConnectionRecovery(d.RegistryID, sleepIdleReason, d.Location); err != nil {
				t.Fatal(err)
			}
		}
		now := time.Now()
		if err := c.trySleepRecovery(context.Background(), d, now); err != nil {
			t.Fatal(err)
		}
		if err := c.trySleepRecovery(context.Background(), d, now.Add(6*time.Second)); err != nil {
			t.Fatal(err)
		}
		if *calls != cycle+1 {
			t.Fatal("cycle did not get exactly one attempt", *calls)
		}
		for i := 0; i < 5; i++ {
			if err := c.trySleepRecovery(context.Background(), d, now.Add(7*time.Second)); err != nil {
				t.Fatal(err)
			}
		}
		if *calls != cycle+1 {
			t.Fatal("cycle repeated mutation")
		}
		if err := c.Store.Event("receive_connection_restored", "", archive.M{"id": c.recoveryID, "inventory_verified": true}); err != nil {
			t.Fatal(err)
		}
		c.recoveryID = ""
		c.recoveryCheckpoint = nil
		d.RegistryID++
	}
}
