package runtime

import (
	"context"
	"errors"
	"fmt"
	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"github.com/iniwex5/vohive/product/djisms-core/discovery"
	"time"
)

var errSleepRecoveryDeferred = errors.New("sleep recovery no longer needed at this boundary")

const sleepIdleReason = "system sleep after receive-only session closed; fresh host verification required on wake"
const sleepRecoveryGrace = 5 * time.Second
const sleepRecoveryTimeout = 45 * time.Second

func (c *Core) sleepRecoveryIntent() (archive.M, error) {
	if c.recoveryID == "" {
		return nil, nil
	}
	e, err := c.Store.LatestEvent("sleep_usb_reenumeration_intent")
	if err != nil {
		return nil, err
	}
	if e == nil || e["checkpoint"] != c.recoveryID {
		return nil, nil
	}
	return e, nil
}
func (c *Core) sleepRecoveryDeadline() error {
	e, err := c.sleepRecoveryIntent()
	if err != nil || e == nil {
		return err
	}
	text, ok := e["deadline"].(string)
	if !ok {
		return errors.New("invalid sleep recovery deadline")
	}
	deadline, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return err
	}
	if time.Now().After(deadline) {
		return errors.New("USB reenumeration did not finish verified recovery within 45 seconds; no retry")
	}
	return nil
}

// Called only before any status/receive/purge acquisition. A durable SLEEP
// checkpoint proves the previous receive session closed at a safe boundary.
func (c *Core) trySleepRecovery(ctx context.Context, d discovery.Device, now time.Time) error {
	cp := c.recoveryCheckpoint
	if cp == nil || !cp.Sleep || cp.ID != c.recoveryID || cp.Location == 0 || cp.Location != d.Location || cp.RegistryID != d.RegistryID || c.isPaused() || ctx.Err() != nil {
		return nil
	}
	if e, err := c.sleepRecoveryIntent(); err != nil || e != nil {
		return err
	}
	if c.deps.mediaInactive == nil || c.deps.reenumerate == nil {
		return nil
	}
	if e := discovery.Validate(d); e != nil {
		return e
	}
	for _, f := range d.Interfaces {
		if f.Number < 4 && f.Owner != "" {
			return errors.New("non-ECM interface occupied during sleep recovery")
		}
	}
	inactive, e := c.deps.mediaInactive(d)
	if e != nil {
		return e
	}
	if !inactive {
		c.recoveryInactiveSince = time.Time{}
		return nil
	}
	if c.recoveryInactiveSince.IsZero() {
		c.recoveryInactiveSince = now
		return nil
	}
	if now.Sub(c.recoveryInactiveSince) < sleepRecoveryGrace {
		return nil
	}
	totals, e := c.Store.Summary()
	if e != nil {
		return e
	}
	if totals["unfinished_delete_attempts"] != 0 {
		return errors.New("unfinished deletion blocks host reenumeration")
	}
	c.update(func(s *State) { s.Phase = "reconnecting"; s.Detail = "正在恢复睡眠后中断的模块连接" })
	attempt := nonce()
	result, operationError := c.deps.reenumerate(d, func() error {
		if ctx.Err() != nil || c.isPaused() {
			return errSleepRecoveryDeferred
		}
		inactive, e := c.deps.mediaInactive(d)
		if e != nil {
			return e
		}
		if !inactive {
			return errSleepRecoveryDeferred
		}
		return c.Store.Event("sleep_usb_reenumeration_intent", "", archive.M{"checkpoint": cp.ID, "attempt": attempt, "registry_id": d.RegistryID, "location": d.Location, "device": d, "options": 0, "max_attempts": 1, "deadline": now.Add(sleepRecoveryTimeout).UTC().Format(time.RFC3339Nano)})
	})
	if errors.Is(operationError, errSleepRecoveryDeferred) {
		c.recoveryInactiveSince = time.Time{}
		return nil
	}
	event := archive.M{"checkpoint": cp.ID, "attempt": attempt, "result": result, "success": operationError == nil}
	if operationError != nil {
		event["error"] = operationError.Error()
	}
	if e = c.Store.Event("sleep_usb_reenumeration_result", "", event); e != nil {
		return e
	}
	if operationError != nil {
		return fmt.Errorf("sleep recovery stopped without retry: %w", operationError)
	}
	return nil
}
