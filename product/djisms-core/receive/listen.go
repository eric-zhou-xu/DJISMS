package receive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/iniwex5/vohive/internal/smsreceive"
	"time"
)

type Sink interface {
	Save(string, any) error
	Message(Notice, smsreceive.RawMessage) error
	Direct(Direct) error
	Snapshot([]smsreceive.RawMessage) error
	Ready(int) error
	Pulse(int) error
}
type Config struct {
	// Yield is checked only between complete responses, after all queued CMTIs.
	// It cannot interrupt a numeric read or discard a pending notification.
	Yield          func() bool
	KnownIndices   map[int]bool
	KnownPDUHashes map[int]string
	ExpectedUsed   int
}
type Summary struct {
	InitialUsed  int    `json:"initial_used"`
	ReadIndices  []int  `json:"read_indices"`
	IgnoredKnown []int  `json:"known_index_notifications"`
	FinalUsed    int    `json:"final_used"`
	Reconciled   bool   `json:"settings_reconciled"`
	Closed       bool   `json:"closed"`
	Error        string `json:"error,omitempty"`
	Forensic     string `json:"forensic_response_attribution"`
	// Interrupted is granted only at an idle, fully classified receive boundary;
	// native closure/release must be proven and session_end must be durable.
	Interrupted bool `json:"recoverable_idle_interruption"`
	// Explicitly distinguish logical release after removal from an OK close.
	RemovedClosure bool `json:"released_after_device_removal,omitempty"`
}

// Listen owns a serial IF2 session. Only an observed, durably recorded ME CMTI
// grants a numeric CMGR; retries, polling lists and arbitrary writes do not exist.
func (t *Transport) Listen(ctx context.Context, cfg Config, sink Sink) (r Summary, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r = Summary{ReadIndices: []int{}, IgnoredKnown: []int{}, Forensic: "INCONCLUSIVE"}
	if !t.connected || t.closed || t.b == nil {
		return r, errClosed
	}
	needsRemovalProof := false
	defer func() {
		e := t.closeLocked()
		var removed *releasedDeviceGone
		if r.Interrupted && errors.As(e, &removed) {
			r.RemovedClosure = true
			e = nil // all references released and old device proven absent
		}
		if needsRemovalProof && !r.RemovedClosure {
			r.Interrupted = false
		}
		r.Closed = e == nil
		if e != nil {
			r.Interrupted = false
		}
		err = errors.Join(err, e)
		if err != nil {
			r.Error = err.Error()
		}
		if sink != nil {
			saveErr := sink.Save("session_end", r)
			if saveErr != nil {
				r.Interrupted = false
			}
			err = errors.Join(err, saveErr)
		}
	}()
	if sink == nil || cfg.ExpectedUsed != len(cfg.KnownIndices) || len(cfg.KnownPDUHashes) != len(cfg.KnownIndices) || cfg.ExpectedUsed < 0 || cfg.ExpectedUsed > 23 {
		return r, errors.New("missing sink or invalid frozen starting inventory")
	}
	p := &framer{}
	reads := 0
	var partialSince time.Time
	idleReceive := false
	receive := func(c context.Context) (bool, error) {
		wait, e := boundedWait(c, 200*time.Millisecond)
		if e != nil {
			return false, e
		}
		buf := make([]byte, 512)
		d, re := t.b.readDiagnostic(buf, wait)
		reads++
		valid := re == nil && d.ReturnCode == 0 && d.CountValid && d.ActualBytes != nil && *d.ActualBytes == d.RawSize && d.RawSize <= 512
		raw := ""
		if valid {
			raw = hex.EncodeToString(buf[:d.RawSize])
		}
		observed := time.Now().UTC().Format(time.RFC3339Nano)
		ev := struct {
			Observed   string         `json:"observed_utc"`
			Number     int            `json:"number"`
			Offset     int            `json:"stream_offset"`
			Diagnostic ReadDiagnostic `json:"diagnostic"`
			Hex        string         `json:"valid_hex"`
		}{observed, reads, p.offset, d, raw}
		if e = sink.Save("usb_read", ev); e != nil {
			return false, e
		} // fsync BEFORE framing
		if !valid {
			if d.Category == "timeout" && (d.ReturnCode == ioTimeout || d.ReturnCode == usbTransactionTimeout) {
				return false, nil
			}
			// IOKit abort/no-device/not-attached can end an otherwise idle read.
			// Never grant recovery in a query, partial frame or queued CMTI.
			if idleReceive && p.boundary() && p.active == nil && len(p.notices) == 0 && len(p.directs) == 0 &&
				(d.ReturnCode == 0xe00002eb || d.ReturnCode == 0xe00002c0 || d.ReturnCode == 0xe00002d9 || d.ReturnCode == 0xe00002ed) {
				// NotResponding alone is ambiguous. Recovery additionally requires the
				// typed NoDevice close + fresh absence proof after native release.
				needsRemovalProof = d.ReturnCode == 0xe00002ed
				r.Interrupted = true
			}
			return false, fmt.Errorf("read fault: %s: %v", d.ReturnHex, re)
		}
		p.when = observed
		parseErr := p.feed(buf[:d.RawSize])
		for _, f := range p.asynchronous {
			if e = sink.Save("asynchronous_urc", f); e != nil {
				return false, e
			}
		}
		p.asynchronous = nil
		for _, d := range p.directs {
			if e = sink.Direct(d); e != nil {
				return false, e
			}
		}
		p.directs = nil
		if parseErr != nil {
			return false, parseErr
		}
		if !p.boundary() {
			if partialSince.IsZero() {
				partialSince = time.Now()
			}
		} else {
			partialSince = time.Time{}
		}
		return true, nil
	}
	query := func(c context.Context, cmd Command) (Response, error) {
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
		wait, e := boundedWait(c, 250*time.Millisecond)
		if e != nil {
			return Response{}, e
		}
		if e = t.b.write(cmd, wait); e != nil {
			return Response{}, e
		}
		if e = sink.Save("out_succeeded", cmd); e != nil {
			return Response{}, e
		}
		budget := 2 * time.Second
		maxReads, maxBytes := 32, 4096
		if cmd.Kind == 5 {
			budget = 10 * time.Second
			maxReads, maxBytes = 128, 65536
		}
		qctx, cancel := context.WithTimeout(c, budget)
		defer cancel()
		startReads := reads
		startBytes := p.offset
		for reads-startReads < maxReads && p.offset-startBytes < maxBytes {
			valid, e := receive(qctx)
			if e != nil {
				return Response{}, e
			}
			if !valid {
				return Response{}, errors.New("response timeout; no retry")
			}
			if p.active.Done && p.boundary() {
				response, e := p.finish()
				if e == nil {
					e = sink.Save("response", response)
				}
				return response, e
			}
		}
		return Response{}, errors.New("response budget exhausted")
	}
	// A single initial sample is evidence only; timeout does not prove empty RX.
	if _, e := receive(ctx); e != nil {
		return r, e
	}
	for !p.boundary() {
		if time.Since(partialSince) > 2*time.Second {
			return r, errors.New("initial partial frame")
		}
		if _, e := receive(ctx); e != nil {
			return r, e
		}
	}
	pre := []Response{}
	for k := uint8(1); k <= 4; k++ {
		a, e := query(ctx, Command{Kind: k})
		if e != nil {
			return r, e
		}
		pre = append(pre, a)
	}
	r.InitialUsed, err = settings(pre)
	if err != nil {
		return r, err
	}
	if r.InitialUsed < cfg.ExpectedUsed {
		return r, errors.New("inventory differs from frozen baseline; no read/list fallback")
	}
	snapshot, e := query(ctx, Command{Kind: 5})
	if e != nil {
		return r, e
	}
	if e = sink.Snapshot(snapshot.Messages); e != nil {
		return r, e
	}
	after, e := query(ctx, Command{Kind: 6})
	if e != nil {
		return r, e
	}
	used, e := storage(after)
	if e != nil {
		return r, e
	}
	if used != r.InitialUsed || len(snapshot.Messages) != used {
		return r, errors.New("snapshot count changed during capture; preserve and stop")
	}
	baseline := map[int]bool{}
	for _, m := range snapshot.Messages {
		baseline[m.Index] = true
		if want, known := cfg.KnownPDUHashes[m.Index]; known && fmt.Sprintf("%x", sha256.Sum256([]byte(m.PDU))) != want {
			return r, errors.New("previous PDU changed at known index")
		}
	}
	for idx := range cfg.KnownIndices {
		if !baseline[idx] {
			return r, errors.New("known original record missing")
		}
	}
	if err = sink.Ready(r.InitialUsed); err != nil {
		return r, err
	}
	seen := map[int]bool{}
	lastPulse := time.Now()
	for ctx.Err() == nil {
		if p.boundary() && len(p.notices) == 0 && cfg.Yield != nil && cfg.Yield() {
			break
		}
		if p.boundary() && len(p.notices) > 0 {
			n := p.notices[0]
			p.notices = p.notices[1:]
			if err = sink.Save("cmti", n); err != nil {
				return r, err
			}
			if baseline[n.Index] {
				r.IgnoredKnown = append(r.IgnoredKnown, n.Index)
				if err = sink.Save("known_index_not_reread", n); err != nil {
					return r, err
				}
				continue
			}
			if seen[n.Index] {
				if err = sink.Save("duplicate_notification_not_reread", n); err != nil {
					return r, err
				}
				continue
			}
			seen[n.Index] = true // reserve before write: failed CMGR is never retried
			a, e := query(ctx, Command{Kind: 7, Index: n.Index})
			if e != nil {
				return r, e
			}
			if len(a.Messages) != 1 {
				return r, errors.New("missing raw message")
			}
			if err = sink.Message(n, a.Messages[0]); err != nil {
				return r, err
			}
			r.ReadIndices = append(r.ReadIndices, n.Index)
			if err = sink.Pulse(len(r.ReadIndices)); err != nil {
				return r, err
			}
			continue
		}
		idleReceive = true
		_, readErr := receive(ctx)
		idleReceive = false
		if e := readErr; e != nil {
			if r.Interrupted {
				return r, e
			}
			if ctx.Err() != nil {
				break
			}
			return r, e
		}
		if !partialSince.IsZero() && time.Since(partialSince) > 2*time.Second {
			return r, errors.New("partial frame exceeded two seconds; retained without flush")
		}
		if time.Since(lastPulse) >= 5*time.Second {
			if err = sink.Pulse(len(r.ReadIndices)); err != nil {
				return r, err
			}
			lastPulse = time.Now()
		}
	}
	// Graceful stop reconciles settings. Fault paths above never issue more OUT.
	if !p.boundary() || len(p.notices) > 0 {
		return r, errors.New("stopped with preserved pending fragment/notification")
	}
	stopctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	post := []Response{}
	for k := uint8(8); k <= 11; k++ {
		a, e := query(stopctx, Command{Kind: k})
		if e != nil {
			return r, e
		}
		post = append(post, a)
	}
	final := []Response{post[1], post[0], post[2], post[3]}
	r.FinalUsed, err = settings(final)
	if err != nil {
		return r, err
	}
	if r.FinalUsed != r.InitialUsed+len(r.ReadIndices) || len(p.notices) > 0 {
		return r, errors.New("storage count changed beyond captured new indices; preserve, no retry")
	}
	if pre[3].Lines[0] != post[3].Lines[0] {
		return r, errors.New("SMS service capabilities changed")
	}
	r.Reconciled = true
	return r, nil
}
