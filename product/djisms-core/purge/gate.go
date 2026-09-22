package purge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/iniwex5/vohive/internal/smsreceive"
	"time"
)

type Config struct {
	TargetIndex    int            `json:"target_index"`
	TargetHash     string         `json:"target_hash"`
	ExpectedHashes map[int]string `json:"expected_hashes"`
}
type Predelete struct {
	Inventory         []smsreceive.RawMessage `json:"inventory"`
	Target            smsreceive.RawMessage   `json:"target"`
	TargetObservedUTC string                  `json:"target_observed_utc"`
	Storage           string                  `json:"storage"`
	Index             int                     `json:"index"`
	PDUHash           string                  `json:"pdu_bytes_sha256"`
}
type Summary struct {
	InitialUsed     int    `json:"initial_used"`
	FinalUsed       int    `json:"final_used"`
	DeleteAttempted bool   `json:"delete_attempted"`
	DeleteAccepted  bool   `json:"delete_accepted"`
	DeleteConfirmed bool   `json:"delete_confirmed"`
	Reconciled      bool   `json:"settings_reconciled"`
	Closed          bool   `json:"closed"`
	Error           string `json:"error,omitempty"`
	Forensic        string `json:"forensic_response_attribution"`
}
type Sink interface {
	Save(string, any) error
	Authorize(context.Context, Predelete) error
}

func hashMessage(m smsreceive.RawMessage) (string, error) {
	b, e := hex.DecodeString(m.PDU)
	if e != nil || len(b) < 2 || int(b[0])+1 >= len(b) || len(b)-1-int(b[0]) != m.TPDULength {
		return "", errors.New("PDU length/SMSC/declared TPDU mismatch")
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
}
func validateConfig(c Config) error {
	if len(c.ExpectedHashes) < 1 || len(c.ExpectedHashes) > 23 || c.TargetIndex < 0 || c.TargetIndex > 22 || c.ExpectedHashes[c.TargetIndex] != c.TargetHash {
		return errors.New("invalid approved target/inventory")
	}
	for i, h := range c.ExpectedHashes {
		b, e := hex.DecodeString(h)
		if i < 0 || i > 22 || e != nil || len(b) != 32 {
			return errors.New("invalid slot/hash")
		}
	}
	return nil
}
func reconcileInventory(ms []smsreceive.RawMessage, c Config, after bool) error {
	want := len(c.ExpectedHashes)
	if after {
		want--
	}
	if len(ms) != want {
		return errors.New("inventory count changed")
	}
	seen := map[int]bool{}
	for _, m := range ms {
		expected, ok := c.ExpectedHashes[m.Index]
		if !ok || seen[m.Index] || (after && m.Index == c.TargetIndex) {
			return errors.New("unexpected/duplicate/deleted index present")
		}
		seen[m.Index] = true
		h, e := hashMessage(m)
		if e != nil {
			return e
		}
		if h != expected {
			return errors.New("existing index/PDU changed")
		}
	}
	for i := range c.ExpectedHashes {
		if after && i == c.TargetIndex {
			continue
		}
		if !seen[i] {
			return errors.New("existing index missing")
		}
	}
	return nil
}

// Purge is one fixed, single-use state machine. No index, raw command, retry,
// setter, bulk flag or next-candidate API is exposed.
func (t *Transport) Purge(ctx context.Context, c Config, sink Sink) (r Summary, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r.Forensic = "INCONCLUSIVE" // preserves accepted timeout-attribution limitation
	if !t.connected || t.closed || t.b == nil {
		return r, errClosed
	}
	defer func() {
		e := t.closeLocked()
		r.Closed = e == nil
		err = errors.Join(err, e)
		if err != nil {
			r.Error = err.Error()
		}
		if sink != nil {
			err = errors.Join(err, sink.Save("session_end", r))
		}
	}()
	if sink == nil {
		return r, errors.New("missing durable sink")
	}
	if err = validateConfig(c); err != nil {
		return r, err
	}
	p := &framer{}
	readCount := 0
	query := func(kind uint8) (Response, error) {
		cmd := Command{Kind: kind}
		if kind == 7 || kind == 9 {
			cmd.Index = c.TargetIndex
		}
		wire, e := cmd.wire()
		if e != nil {
			return Response{}, e
		}
		if e = p.begin(cmd); e != nil {
			return Response{}, e
		}
		if e = sink.Save("out_intent", map[string]any{"command": wire, "hex": hex.EncodeToString([]byte(wire + "\r"))}); e != nil {
			return Response{}, e
		}
		wait, e := boundedWait(ctx, 250*time.Millisecond)
		if e != nil {
			return Response{}, e
		}
		if kind == 9 {
			if e = sink.Save("delete_attempt", map[string]any{"command": wire, "index": c.TargetIndex, "storage": "ME", "max_attempts": 1}); e != nil {
				return Response{}, e
			}
		}
		if e = ctx.Err(); e != nil {
			return Response{}, e
		}
		if kind == 9 {
			r.DeleteAttempted = true
		}
		if e = t.b.write(cmd, wait); e != nil {
			return Response{}, e
		}
		if e = sink.Save("out_succeeded", cmd); e != nil {
			return Response{}, e
		}
		budget := 2 * time.Second
		maxReads, maxBytes := 32, 4096
		if kind == 5 || kind == 11 {
			budget = 10 * time.Second
			maxReads, maxBytes = 128, 65536
		}
		qctx, cancel := context.WithTimeout(ctx, budget)
		defer cancel()
		start := p.offset
		for n := 0; n < maxReads && p.offset-start < maxBytes; n++ {
			wait, e := boundedWait(qctx, 250*time.Millisecond)
			if e != nil {
				return Response{}, e
			}
			buf := make([]byte, 512)
			d, re := t.b.readDiagnostic(buf, wait)
			readCount++
			valid := re == nil && d.ReturnCode == 0 && d.CountValid && d.ActualBytes != nil && *d.ActualBytes == d.RawSize && d.RawSize <= 512
			raw := ""
			if valid {
				raw = hex.EncodeToString(buf[:d.RawSize])
			}
			observed := time.Now().UTC().Format(time.RFC3339Nano)
			if e = sink.Save("usb_read", map[string]any{"observed_utc": observed, "number": readCount, "stream_offset": p.offset, "diagnostic": d, "valid_hex": raw}); e != nil {
				return Response{}, e
			}
			// Unlike a listener there is no idle/pre-read timeout. Any command read timeout
			// or error stops immediately. Native reported bytes on failure are not trusted.
			if !valid {
				return Response{}, fmt.Errorf("read failure/timeout; no retry: %s %v", d.ReturnHex, re)
			}
			p.when = observed
			parseErr := p.feed(buf[:d.RawSize])
			for _, f := range p.asynchronous {
				if e = sink.Save("asynchronous_urc", f); e != nil {
					return Response{}, e
				}
			}
			p.asynchronous = nil
			for _, d := range p.directs {
				if e = sink.Save("direct_handoff_raw", d); e != nil {
					return Response{}, e
				}
				receiver, ok := sink.(interface{ DirectPurge(Direct) error })
				if !ok {
					return Response{}, errors.New("durable direct handoff unavailable")
				}
				if e = receiver.DirectPurge(d); e != nil {
					return Response{}, e
				}
			}
			p.directs = nil
			if parseErr != nil {
				return Response{}, parseErr
			}
			if len(p.notices) > 0 {
				if e = sink.Save("unexpected_cmti", p.notices); e != nil {
					return Response{}, e
				}
				return Response{}, errors.New("storage notification during gate; stop")
			}
			if p.active.Done && p.boundary() {
				response, e := p.finish()
				if e == nil {
					e = sink.Save("response", response)
				}
				return response, e
			}
		}
		return Response{}, errors.New("response budget exhausted; no retry")
	}
	var pre []Response
	for k := uint8(1); k <= 4; k++ {
		q, e := query(k)
		if e != nil {
			return r, e
		}
		pre = append(pre, q)
	}
	r.InitialUsed, err = settings(pre)
	if err != nil {
		return r, err
	}
	if r.InitialUsed != len(c.ExpectedHashes) {
		return r, errors.New("initial inventory count differs")
	}
	inventory, e := query(5)
	if e != nil {
		return r, e
	}
	if e = reconcileInventory(inventory.Messages, c, false); e != nil {
		return r, e
	}
	if e = sink.Save("pre_inventory_raw", inventory.Messages); e != nil {
		return r, e
	}
	cp, e := query(6)
	if e != nil {
		return r, e
	}
	used, e := storage(cp)
	if e != nil {
		return r, e
	}
	if used != len(c.ExpectedHashes) {
		return r, errors.New("inventory changed during capture")
	}
	target, e := query(7)
	if e != nil {
		return r, e
	}
	stamp := time.Now()
	h, e := hashMessage(target.Messages[0])
	if e != nil {
		return r, e
	}
	if target.Messages[0].Index != c.TargetIndex || h != c.TargetHash {
		return r, errors.New("fresh target PDU does not match approval")
	}
	cp, e = query(8)
	if e != nil {
		return r, e
	}
	used, e = storage(cp)
	if e != nil {
		return r, e
	}
	if used != len(c.ExpectedHashes) {
		return r, errors.New("selected store/count changed before delete")
	}
	check := Predelete{inventory.Messages, target.Messages[0], stamp.UTC().Format(time.RFC3339Nano), "ME", c.TargetIndex, h}
	if e = sink.Save("predelete_verified", check); e != nil {
		return r, e
	}
	authctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	e = sink.Authorize(authctx, check)
	cancel()
	if e != nil {
		return r, e
	}
	if time.Since(stamp) > 30*time.Second {
		return r, errors.New("fresh target read expired; no delete")
	}
	if _, e = query(9); e != nil {
		return r, e
	}
	r.DeleteAccepted = true
	cp, e = query(10)
	if e != nil {
		return r, e
	}
	used, e = storage(cp)
	if e != nil {
		return r, e
	}
	if used != len(c.ExpectedHashes)-1 {
		return r, errors.New("post-delete count differs")
	}
	after, e := query(11)
	if e != nil {
		return r, e
	}
	if e = sink.Save("post_inventory_raw", after.Messages); e != nil {
		return r, e
	}
	if e = reconcileInventory(after.Messages, c, true); e != nil {
		return r, e
	}
	cp, e = query(12)
	if e != nil {
		return r, e
	}
	r.FinalUsed, e = storage(cp)
	if e != nil {
		return r, e
	}
	if r.FinalUsed != len(c.ExpectedHashes)-1 {
		return r, errors.New("post inventory count changed")
	}
	format, e := query(13)
	if e != nil {
		return r, e
	}
	notifications, e := query(14)
	if e != nil {
		return r, e
	}
	service, e := query(15)
	if e != nil {
		return r, e
	}
	if _, e = settings([]Response{format, cp, notifications, service}); e != nil {
		return r, e
	}
	for i, pair := range [][2]Response{{pre[0], format}, {pre[2], notifications}, {pre[3], service}} {
		if pair[0].Lines[0] != pair[1].Lines[0] {
			return r, fmt.Errorf("setting %d changed", i)
		}
	}
	r.Reconciled = true
	r.DeleteConfirmed = true
	return r, nil
}
