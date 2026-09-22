package runtime

import (
	"context"
	"errors"
	"fmt"
	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"github.com/iniwex5/vohive/product/djisms-core/discovery"
	"github.com/iniwex5/vohive/product/djisms-core/host"
	"github.com/iniwex5/vohive/product/djisms-core/purge"
	"time"
)

type purgeFailure struct{ error }
type purgeSession struct {
	*session
	target  record
	index   int
	attempt string
	before  host.Snapshot
	intent  bool
}

func (c *Core) purgeOne(ctx context.Context, d discovery.Device, inventory map[int]record, index int) (err error) {
	defer func() {
		if err != nil {
			err = purgeFailure{err}
		}
	}()
	if e := ctx.Err(); e != nil {
		return e
	}
	operationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer cancel()
	ctx = operationCtx
	c.update(func(s *State) {
		s.Phase = "archiving"
		s.Detail = "档案已保存，正在逐条核验并清理模块缓存"
	})
	before, e := c.deps.capture(ctx, d)
	if e != nil {
		return e
	}
	p := &purgeSession{session: &session{core: c, device: d, id: nonce(), inventory: map[int]record{}}, target: inventory[index], index: index, attempt: "product-purge-" + nonce(), before: before}
	if e = p.Save("host_before", before); e != nil {
		return e
	}
	cfg := purge.Config{TargetIndex: index, TargetHash: p.target.hash, ExpectedHashes: map[int]string{}}
	for i, r := range inventory {
		cfg.ExpectedHashes[i] = r.hash
	}
	if e = p.Save("purge_plan", cfg); e != nil {
		return e
	}
	tr := c.deps.purge(d)
	if e = tr.Connect(ctx); e != nil {
		return e
	}
	result, operationErr := tr.Purge(ctx, cfg, p)
	// Reconciliation never sends AT or retries any command, even after a fault.
	hostErr := p.reconcile(context.WithoutCancel(ctx), before)
	if operationErr == nil && hostErr == nil && result.DeleteConfirmed && result.Reconciled && result.Closed && p.intent {
		if _, e = c.Store.Verify(p.target.receipt); e != nil {
			return e
		}
		if e = c.Store.Event("delete_result", p.target.receipt, archive.M{"attempt_id": p.attempt, "state": "confirmed", "response": "exact CMGD echo+OK, target absent, every other hash unchanged, counts/settings/USB/ECM/HTTPS reconciled"}); e != nil {
			return e
		}
		if e = c.Store.Event("delete_confirmation", p.target.receipt, archive.M{"attempt_id": p.attempt, "storage": "ME", "index": index, "raw_pdu_sha256": p.target.hash, "host_reconciliation": "PASS"}); e != nil {
			return e
		}
		c.update(func(s *State) { s.Used = result.FinalUsed })
		return nil
	}
	err = errors.Join(operationErr, hostErr)
	if err == nil {
		err = errors.New("purge lacked complete confirmation")
	}
	if p.intent {
		err = errors.Join(err, c.Store.Event("delete_result", p.target.receipt, archive.M{"attempt_id": p.attempt, "state": "unknown", "response": err.Error()}))
	}
	return err
}
func (p *purgeSession) Authorize(ctx context.Context, fresh purge.Predelete) error {
	if p.intent {
		return errors.New("purge intent already consumed")
	}
	if fresh.Storage != "ME" || fresh.Index != p.index || fresh.PDUHash != p.target.hash {
		return errors.New("fresh storage/index/hash mismatch")
	}
	original, e := p.core.Store.Receipt(p.target.receipt)
	if e != nil {
		return e
	}
	if original["storage"] != "ME" || original["storage_index"] != int64(p.index) || original["pdu_bytes_sha256"] != p.target.hash {
		return errors.New("permanent target identity mismatch")
	}
	if ok, e := p.core.Store.Eligible(p.target.receipt); e != nil || !ok {
		return errors.Join(e, errors.New("target archive not eligible"))
	}
	// Preserve every fresh observation, including the final CMGR, before deletion.
	for _, m := range fresh.Inventory {
		if _, e = p.ingest(m, "predelete_snapshot", fresh.TargetObservedUTC, false); e != nil {
			return e
		}
	}
	finalID, e := p.ingest(fresh.Target, "predelete_target", fresh.TargetObservedUTC, false)
	if e != nil {
		return e
	}
	if ok, e := p.core.Store.Eligible(finalID); e != nil || !ok {
		return errors.Join(e, errors.New("fresh target not eligible"))
	}
	ds, e := p.core.deps.snapshot()
	if e != nil {
		return e
	}
	d, e := discovery.Single(ds)
	if e != nil {
		return e
	}
	during, e := p.core.deps.capture(ctx, d)
	if e != nil {
		return e
	}
	if e = p.Save("host_during", during); e != nil {
		return e
	}
	if e = host.CompareDuring(p.before, during); e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	stamp, e := time.Parse(time.RFC3339Nano, fresh.TargetObservedUTC)
	if e != nil || time.Since(stamp) > 25*time.Second {
		return errors.New("fresh target observation expired before intent")
	}
	if e = p.core.Store.Event("delete_intent", p.target.receipt, archive.M{"attempt_id": p.attempt, "storage": "ME", "index": p.index, "pdu_sha256": p.target.hash, "connection_id": p.id, "mode": "approved_live", "fresh_receipt": finalID, "confirmed_scope": fmt.Sprintf("ME/%d exact current PDU only", p.index)}); e != nil {
		return e
	}
	p.intent = true
	return nil
}
