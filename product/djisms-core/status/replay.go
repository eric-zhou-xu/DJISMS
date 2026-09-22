package status

import (
	"context"
	"encoding/hex"
	"errors"
	"github.com/iniwex5/vohive/product/djisms-core/atproto"
)

func queryFor(command string) (Query, error) {
	for i, d := range definitions {
		if d.command == command {
			return Query(i + 1), nil
		}
	}
	return 0, errors.New("not a fixed status query")
}
func replayStream(r Report) (*atStream, error) {
	q, e := queryFor(r.Command)
	if e != nil {
		return nil, e
	}
	if r.RequestID == 0 || r.Interface != 2 || r.PlannedOutHex != hex.EncodeToString([]byte(r.Command+"\r")) {
		return nil, errors.New("invalid query provenance")
	}
	p := newATStream(r.RequestID, q)
	written := false
	offset := 0
	for i, read := range r.Reads {
		if read.Index != i+1 || read.StreamOffset != offset {
			return p, errors.New("noncontiguous query reads")
		}
		if read.Phase == "after_write" && !written {
			if !r.WriteAttempted {
				return p, errors.New("read without successful OUT evidence")
			}
			if e = p.markWritten(); e != nil {
				return p, e
			}
			written = true
		} else if read.Phase != "before_write" && read.Phase != "after_write" {
			return p, errors.New("invalid read phase")
		}
		if written && read.Phase == "before_write" {
			return p, errors.New("read phase regression")
		}
		d := read.Diagnostic
		if read.ValidHex == "" {
			if d.Category == "timeout" && (d.ReturnCode == ioTimeout || d.ReturnCode == usbTransactionTimeout) {
				continue
			}
			if d.ReturnCode == 0 && d.CountValid && d.ActualBytes != nil && *d.ActualBytes == 0 && d.RawSize == 0 {
				continue
			}
			return p, errors.New("untrusted query read")
		}
		b, e := hex.DecodeString(read.ValidHex)
		if e != nil {
			return p, e
		}
		if d.ReturnCode != 0 || !d.CountValid || d.ActualBytes == nil || *d.ActualBytes != d.RawSize || len(b) != int(d.RawSize) || len(b) > 512 {
			return p, errors.New("invalid preserved read length")
		}
		offset += len(b)
		if e = p.feed(b); e != nil {
			return p, e
		}
	}
	if !written && r.WriteAttempted {
		if e = p.markWritten(); e != nil {
			return p, e
		}
	}
	return p, nil
}

// ReplayFrames derives complete frames only from the recorded bytes. A later
// fault does not erase earlier complete frames; callers must retain the error.
func ReplayFrames(r Report) ([]atproto.Frame, error) {
	p, e := replayStream(r)
	if p == nil {
		return nil, e
	}
	return append([]atproto.Frame(nil), p.frames...), e
}

// ContinueRead is an explicit read-only continuation of one preserved request.
// It issues NO OUT, requires original durable write-intent evidence and closes its
// interface on every path. It cannot be used for an arbitrary command.
func (t *Transport) ContinueRead(ctx context.Context, seed Report, record func(Report) error) (r PlanReport, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.connected || t.closed || record == nil {
		return r, errClosed
	}
	defer func() {
		e := t.closeLocked()
		r.CloseSucceeded = e == nil
		err = errors.Join(err, e)
		if err != nil {
			r.Error = err.Error()
		} else {
			r.EngineeringOutcome = "PASS"
		}
	}()
	q, e := queryFor(seed.Command)
	if e != nil {
		return r, e
	}
	if !seed.WriteAttempted {
		return r, errors.New("cannot continue without durable OUT intent")
	}
	p, e := replayStream(seed)
	if e != nil {
		return r, e
	}
	if p.complete() {
		return r, errors.New("request already complete; no continuation needed")
	}
	a, e := t.executeFrom(ctx, q, record, &seed)
	r.Queries = []Report{a}
	return r, errors.Join(e, record(a))
}
