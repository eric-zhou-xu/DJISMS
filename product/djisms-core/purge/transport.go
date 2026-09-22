// Package smspurgeslot provides a bounded, audited, single-slot purge plan on Interface 2.
package purge

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/iniwex5/vohive/internal/safeusb"
)

// The complete audited hardware profile is invariant; the location is selected
// from the current discovery session and the native layer pins its registry ID.
const reviewedHex = "09020201060100a0fa0904000002ffffff0007058102000200070501020002000904010003ff00000005240010010524010000042402020524060000070583030a000907058202000200070502020002000904020003ff00000005240010010524010000042402020524060000070585030a000907058402000200070503020002000904030003ff00000005240010010524010000042402020524060000070587030a00090705860200020007050402000200080b040202060009090404000102060006052400100105240604050d240f0700000000ea050000000705890310000909040500000a00000009040501020a0000080705880200020007050502000200"

var errTimeout = errors.New("USB read timeout")
var errClosed = errors.New("transport closed or not connected")

// The backend is private. Neither callers nor JSON can choose interfaces/pipes,
// supply wire bytes, or replace the reviewed profile.
type backend interface {
	snapshot() (safeusb.Snapshot, error)
	open() error                        // exactly interface 2, bulk 84/03; validates before and after open
	write(Command, time.Duration) error // native implementation also checks enum
	readDiagnostic([]byte, time.Duration) (ReadDiagnostic, error)
	close() error
}

type Transport struct {
	mu                sync.Mutex
	b                 backend
	connected, closed bool
	location          uint32
}

func checkSnapshot(s safeusb.Snapshot, location ...uint32) error {
	if s.Schema != 1 || len(s.Devices) != 1 {
		return errors.New("expected exactly one recorded device")
	}
	raw, err := hex.DecodeString(reviewedHex)
	if err != nil {
		return err
	}
	expected := safeusb.Device{Vendor: safeusb.VendorID, Product: safeusb.ProductID, BCDDevice: 792, LocationID: 0}
	if err := safeusb.ParseConfiguration(raw, &expected); err != nil {
		return err
	}
	if len(location) == 1 {
		expected.LocationID = location[0]
	}
	d := s.Devices[0]
	d.Address = 0
	if err := safeusb.Validate(d); err != nil {
		return err
	}
	if !reflect.DeepEqual(d, expected) {
		return errors.New("device/port/full composition is not the reviewed ECM profile")
	}
	return nil
}

// Connect issues no AT. The normal app and descriptor commands do not use it.
// BUSY, access denied and mismatches are terminal; no retry or alternate port.
func (t *Transport) Connect(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.connected || t.b == nil {
		return errClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s, err := t.b.snapshot()
	if err != nil {
		return t.fail(err)
	}
	if err = checkSnapshot(s, t.location); err != nil {
		return t.fail(err)
	}
	if err = ctx.Err(); err != nil {
		return t.fail(err)
	}
	if err = t.b.open(); err != nil {
		return t.fail(err)
	}
	// Recheck after acquisition. Native open separately validates its retained
	// parent and every pipe to prevent a location-only time-of-check substitution.
	s, err = t.b.snapshot()
	if err == nil {
		err = checkSnapshot(s, t.location)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return t.fail(err)
	}
	t.connected = true
	return nil
}

func boundedWait(ctx context.Context, max time.Duration) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < time.Millisecond {
			return 0, context.DeadlineExceeded
		}
		if remaining < max {
			max = remaining
		}
	}
	return max, nil
}

func (t *Transport) fail(err error) error { return errors.Join(err, t.closeLocked()) }
func (t *Transport) closeLocked() error {
	if t.closed {
		return nil
	}
	t.closed = true
	t.connected = false
	if t.b == nil {
		return nil
	}
	if err := t.b.close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	return nil
}
func (t *Transport) Close() error { t.mu.Lock(); defer t.mu.Unlock(); return t.closeLocked() }
