package storagecap

import (
	"context"
	"errors"
	"time"
)

type PlanReport struct {
	Policy              string   `json:"policy"`
	EngineeringOutcome  string   `json:"engineering_outcome"`
	ForensicAttribution string   `json:"forensic_response_attribution"`
	Queries             []Report `json:"queries"`
	CloseSucceeded      bool     `json:"close_succeeded"`
	Error               string   `json:"error,omitempty"`
}

// ExecutePlan owns one serial session and closes it on all paths. No arbitrary
// command, subset, restart, retry or receive-only drain API is exposed.
func (t *Transport) ExecutePlan(ctx context.Context, record func(Report) error) (r PlanReport, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r = PlanReport{Policy: unsolicitedPolicy, EngineeringOutcome: "FAIL", ForensicAttribution: "OBSERVED_SESSION_ONLY", Queries: []Report{}}
	if !t.connected || t.closed || t.b == nil {
		return r, errClosed
	}
	defer func() {
		closeErr := t.closeLocked()
		r.CloseSucceeded = closeErr == nil
		err = errors.Join(err, closeErr)
		if err == nil && len(r.Queries) == len(definitions) {
			r.EngineeringOutcome = "PASS"
		}
		if err != nil {
			r.Error = err.Error()
		}
	}()
	if record == nil {
		return r, errors.New("durable evidence recorder is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	for q := Query(1); int(q) <= len(definitions); q++ {
		a, e := t.execute(ctx, q, record)
		r.Queries = append(r.Queries, a)
		if len(a.AttributionIssues) > 0 {
			r.ForensicAttribution = "INCONCLUSIVE"
		}
		// Persist final classified query evidence before considering another OUT.
		saveErr := record(a)
		if e != nil || saveErr != nil {
			return r, errors.Join(e, saveErr)
		}
	}
	return r, nil
}
