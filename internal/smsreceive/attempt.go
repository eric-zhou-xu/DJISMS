package smsreceive

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

type ReadEvidence struct {
	Index        int            `json:"index"`
	Phase        string         `json:"phase"`
	StreamOffset int            `json:"stream_byte_offset"`
	Diagnostic   ReadDiagnostic `json:"diagnostic"`
	ValidHex     string         `json:"valid_hex"`
	Error        string         `json:"error,omitempty"`
}
type Report struct {
	Messages                   []RawMessage   `json:"messages,omitempty"`
	ResponseLines              []string       `json:"response_lines"`
	Policy                     string         `json:"unsolicited_policy"`
	RequestID                  uint64         `json:"host_request_id"`
	Interface                  int            `json:"interface"`
	Command                    string         `json:"command"`
	PlannedOutHex              string         `json:"planned_out_hex"`
	SafeToSend                 bool           `json:"safe_to_send_query"`
	ResponseEvidenceSufficient bool           `json:"forensic_evidence_sufficient"`
	AttributionIssues          []string       `json:"attribution_issues"`
	Outcome                    string         `json:"outcome"`
	ObservedBoundaryClassified bool           `json:"observed_pre_write_boundary_classified"`
	WriteAttempted             bool           `json:"write_attempted"`
	WriteSucceeded             bool           `json:"write_succeeded"`
	EchoMatched                bool           `json:"echo_matched"`
	TerminalOK                 bool           `json:"terminal_ok_observed"`
	Success                    bool           `json:"success"`
	Reads                      []ReadEvidence `json:"reads"`
	Events                     []StreamEvent  `json:"events"`
	StopReason                 string         `json:"stop_reason,omitempty"`
}

var hostRequestCounter atomic.Uint64

// execute is internal: only ExecutePlan may choose the fixed next query.
func (t *Transport) execute(ctx context.Context, q Query, record func(Report) error) (report Report, err error) {
	command, e := q.command()
	if e != nil {
		return report, e
	}
	report = Report{Policy: unsolicitedPolicy, RequestID: hostRequestCounter.Add(1), Interface: 2, Command: command, PlannedOutHex: hex.EncodeToString([]byte(command + "\r")), Reads: []ReadEvidence{}, Events: []StreamEvent{}}
	stream := newATStream(report.RequestID, q)
	evidenceFailure := false
	attributionIssue := func(reason string) { report.AttributionIssues = append(report.AttributionIssues, reason) }
	checkpoint := func() error {
		if record == nil {
			return nil
		}
		report.Events = stream.events
		if e := record(report); e != nil {
			evidenceFailure = true
			return fmt.Errorf("evidence checkpoint failed: %w", e)
		}
		return nil
	}
	defer func() {
		stream.finishFragment("incomplete line at end of attempt")
		report.Events = stream.events
		report.EchoMatched = stream.echo
		report.TerminalOK = stream.terminal
		if err == nil {
			err = stream.validateSuccess()
		}
		if err == nil {
			err = validateResponse(q, stream.payload)
		}
		report.ResponseLines = append([]string(nil), stream.payload...)
		report.Messages = append([]RawMessage(nil), stream.messages...)
		report.ResponseEvidenceSufficient = report.WriteSucceeded && stream.complete() && len(report.AttributionIssues) == 0
		// User accepted forensic ambiguity as nonblocking for engineering progress.
		// Real framing faults still fail validateSuccess; timeout notes alone do not.
		report.Success = err == nil
		report.Outcome = "FAIL"
		if report.Success {
			report.Outcome = "PASS"
		}
		if err != nil {
			report.StopReason = err.Error()
		}
	}()
	if err = ctx.Err(); err != nil {
		return report, err
	}
	reader := t.b
	budget := 2 * time.Second
	maxReads, maxBytes := 32, 4096
	if q == ListMessages {
		budget = 10 * time.Second
		maxReads, maxBytes = 128, 65536
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	totalBytes := 0
	receive := func(phase string, maxWait time.Duration) error {
		if len(report.Reads) >= maxReads || totalBytes >= maxBytes {
			return errors.New("bounded input budget exhausted")
		}
		wait, e := boundedWait(ctx, maxWait)
		if e != nil {
			return e
		}
		size := 512
		if remaining := maxBytes - totalBytes; remaining < size {
			size = remaining
		}
		buf := make([]byte, size)
		d, readErr := reader.readDiagnostic(buf, wait)
		evidence := ReadEvidence{Index: len(report.Reads) + 1, Phase: phase, StreamOffset: totalBytes, Diagnostic: d}
		if readErr != nil {
			evidence.Error = readErr.Error()
		}
		valid := readErr == nil && d.ReturnCode == 0 && d.CountValid && d.ActualBytes != nil && *d.ActualBytes == d.RawSize && d.RawSize <= uint32(len(buf))
		if valid {
			evidence.ValidHex = hex.EncodeToString(buf[:d.RawSize])
			totalBytes += int(d.RawSize)
		}
		report.Reads = append(report.Reads, evidence) // authoritative raw evidence, before classification
		if e = checkpoint(); e != nil {
			return e
		}
		if !valid {
			return fmt.Errorf("input read failed/invalid: IOKit=%s raw_size=%d: %w", d.ReturnHex, d.RawSize, errors.Join(readErr, errors.New("no trustworthy bytes")))
		}
		// Process all bytes already returned, even if unknown input occurs mid-chunk.
		// The parser retains any tail after a fault without guessing its meaning.
		if stream.fault != nil {
			stream.retainTail(buf[:d.RawSize])
			return stream.fault
		}
		if e = stream.feed(buf[:d.RawSize]); e != nil {
			return e
		}
		return ctx.Err()
	}
	// Sampling is evidence collection, NOT a proof of physical RX silence.
	// Extra reads only finish an observed fragment (at most four total), never
	// seek physical silence. Timeout ambiguity remains an audit note; actual framing faults cannot PASS.
	for preReads := 0; preReads < 4; preReads++ {
		if err = receive("before_write", 50*time.Millisecond); err != nil {
			if ctx.Err() != nil {
				return report, ctx.Err()
			}
			if evidenceFailure || errors.Is(err, context.DeadlineExceeded) || len(report.Reads) == 0 {
				return report, err
			}
			last := report.Reads[len(report.Reads)-1].Diagnostic
			// Only a returned timeout or a parser fault is an attribution issue.
			// Invalid SDK counts, transport faults and recorder failures stay STOP.
			if (last.Category == "timeout" && (last.ReturnCode == ioTimeout || last.ReturnCode == usbTransactionTimeout)) || (last.CountValid && stream.fault != nil && errors.Is(err, stream.fault)) {
				attributionIssue(err.Error())
				break
			}
			return report, err
		}
		if stream.boundary() {
			break
		}
		if preReads == 3 {
			attributionIssue("pre-write frame completion budget exhausted")
		}
	}
	report.ObservedBoundaryClassified = stream.boundary() && len(report.AttributionIssues) == 0
	err = nil // pre-write attribution faults are carried explicitly above
	wait, e := boundedWait(ctx, 250*time.Millisecond)
	if e != nil {
		return report, e
	}
	report.SafeToSend = true
	if err = checkpoint(); err != nil {
		report.SafeToSend = false
		return report, err
	}
	// Disk synchronization may consume the remaining context budget.
	wait, e = boundedWait(ctx, 250*time.Millisecond)
	if e != nil {
		report.SafeToSend = false
		return report, e
	}
	report.WriteAttempted = true
	if err = t.b.write(q, wait); err != nil {
		return report, fmt.Errorf("read-only query write failed; no retry: %w", err)
	}
	report.WriteSucceeded = true
	if stream.boundary() {
		if err = stream.markWritten(); err != nil {
			return report, err
		}
	} else {
		// Preserve the pre-write fragment without interpreting its continuation
		// as this command's response. The parser fault remains sticky.
		stream.finishFragment("pre-write fragment crosses write boundary")
		stream.state = "after_write_unattributed"
	}
	if err = checkpoint(); err != nil {
		return report, err
	}
	for {
		if err = receive("after_write", 200*time.Millisecond); err != nil {
			return report, err
		}
		if stream.terminal {
			// No extra read after OK, including to finish a trailing partial URC.
			if err = stream.validateSuccess(); err != nil {
				return report, err
			}
			return report, nil
		}
	}
}
