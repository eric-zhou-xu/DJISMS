package runtime

import (
	"context"
	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"github.com/iniwex5/vohive/product/djisms-core/discovery"
	"github.com/iniwex5/vohive/product/djisms-core/status"
	"testing"
)

type cancelStatusMock struct {
	cancel context.CancelFunc
	unsafe bool
}

func (m *cancelStatusMock) Connect(context.Context) error { return nil }
func (m *cancelStatusMock) ExecutePlan(_ context.Context, save func(status.Report) error) (status.PlanReport, error) {
	m.cancel()
	q := status.Report{Command: "AT+CGMI", Interface: 2, RequestID: 1, PlannedOutHex: "41542b43474d490d", Policy: "readonly-status-demux-v2", StopReason: "context canceled", Outcome: "FAIL", WriteAttempted: m.unsafe}
	if e := save(q); e != nil {
		return status.PlanReport{}, e
	}
	return status.PlanReport{Policy: q.Policy, Error: "context canceled", CloseSucceeded: true, Queries: []status.Report{q}}, context.Canceled
}
func TestTargetedClosedCancelVsUnknownOUT(t *testing.T) {
	for _, unsafe := range []bool{false, true} {
		c, d := testCore(t)
		ctx, cancel := context.WithCancel(context.Background())
		setup(c, d, smsFixture(t), cancel, false)
		c.deps.status = func(discovery.Device) statusTransport { return &cancelStatusMock{cancel, unsafe} }
		c.run(ctx)
		stop, e := c.Store.LatestEvent("core_safety_stop")
		if e != nil || (stop != nil) != unsafe {
			t.Fatal(unsafe, stop, e)
		}
		if !unsafe {
			v, _ := c.Store.LatestEvent("status_cancelled_before_out")
			if v == nil {
				t.Fatal("missing durable cancellation proof")
			}
		}
	}
}
func TestTargetedSMRiskBlocksBeforeUSB(t *testing.T) {
	c, _ := testCore(t)
	c.Store.Event("sim_selection_intent", "", archive.M{"session": "unfinished"})
	c.deps.snapshot = func() ([]discovery.Device, error) { t.Fatal("USB reached despite selection risk"); return nil, nil }
	c.run(context.Background())
	s, _ := c.Current()
	if s.Phase != "safety_stop" {
		t.Fatal(s)
	}
}
