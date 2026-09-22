package receive

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// LegacyRemovalError identifies the pre-fix empty, idle removal failure. This
// review path is engineering-only: it never changes automatic failure policy.
const LegacyRemovalError = "read fault: 0xe00002ed: IOKit read error: 0xe00002ed\nclose: USBInterfaceClose failed: 0xe00002c0"

type ReviewEvent struct {
	Kind string
	Data []byte
}

// ReviewLegacyEmptyRemoval replays every preserved transport fact. Only the
// six initial queries, an empty snapshot, no notices/directs and the precise
// terminal removal fault are accepted. It opens no device and writes nothing.
func ReviewLegacyEmptyRemoval(events []ReviewEvent) error {
	p := &framer{when: "historical-review"}
	pre := []Response{}
	pending := ""
	commands, responses := 0, 0
	aborted, ended, emptySnapshot := false, false, false
	for _, ev := range events {
		if ended {
			return errors.New("evidence after session end")
		}
		if aborted && ev.Kind != "session_end" && ev.Kind != "idle_timeout_summary" {
			return errors.New("transport activity after failure")
		}
		switch ev.Kind {
		case "out_intent":
			if pending != "" || commands >= 6 || responses != commands {
				return errors.New("unexpected OUT")
			}
			var v struct{ Command string }
			if e := json.Unmarshal(ev.Data, &v); e != nil {
				return e
			}
			pending = v.Command
			wire, _ := (Command{Kind: uint8(commands + 1)}).wire()
			if pending != wire {
				return errors.New("unexpected command")
			}
		case "out_succeeded":
			var cmd Command
			if e := json.Unmarshal(ev.Data, &cmd); e != nil {
				return e
			}
			wire, e := cmd.wire()
			if e != nil || pending == "" || pending != wire || int(cmd.Kind) != commands+1 {
				return errors.New("unmatched successful OUT")
			}
			if e = p.begin(cmd); e != nil {
				return e
			}
			pending = ""
			commands++
		case "usb_read":
			var v struct {
				Hex        string         `json:"valid_hex"`
				Diagnostic ReadDiagnostic `json:"diagnostic"`
			}
			if e := json.Unmarshal(ev.Data, &v); e != nil {
				return e
			}
			d := v.Diagnostic
			if d.ReturnCode == 0 {
				raw, e := hex.DecodeString(v.Hex)
				if e != nil {
					return e
				}
				if !d.CountValid || d.ActualBytes == nil || *d.ActualBytes != d.RawSize || int(d.RawSize) != len(raw) || len(raw) > 512 {
					return errors.New("invalid preserved read")
				}
				if e = p.feed(raw); e != nil {
					return e
				}
				if len(p.notices) != 0 || len(p.directs) != 0 {
					return errors.New("pending message evidence")
				}
			} else if d.ReturnCode == 0xe00002ed {
				if pending != "" || p.active != nil || !p.boundary() || commands != 6 || responses != 6 || !emptySnapshot {
					return errors.New("failure not proven empty idle")
				}
				aborted = true
			} else if d.ReturnCode != ioTimeout && d.ReturnCode != usbTransactionTimeout {
				return errors.New("unexpected read failure")
			}
		case "response":
			result, e := p.finish()
			if e != nil {
				return e
			}
			responses++
			if responses <= 4 {
				pre = append(pre, result)
			}
			if responses == 4 {
				used, e := settings(pre)
				if e != nil || used != 0 {
					return errors.New("initial settings or inventory not empty and reviewed")
				}
			}
			if responses == 6 {
				used, e := storage(result)
				if e != nil || used != 0 {
					return errors.New("final query inventory not empty")
				}
			}
			if len(result.Messages) != 0 {
				return errors.New("nonempty received inventory")
			}
		case "snapshot_raw":
			var raw []json.RawMessage
			if e := json.Unmarshal(ev.Data, &raw); e != nil {
				return e
			}
			if len(raw) != 0 || responses != 5 || emptySnapshot {
				return errors.New("snapshot not empty or misplaced")
			}
			emptySnapshot = true
		case "idle_timeout_summary":
			if commands != 6 || responses != 6 {
				return errors.New("idle before readiness")
			}
		case "session_end":
			var end Summary
			if e := json.Unmarshal(ev.Data, &end); e != nil {
				return e
			}
			if !aborted || end.Error != LegacyRemovalError || end.Closed || end.Reconciled || end.InitialUsed != 0 || len(end.ReadIndices) != 0 || len(end.IgnoredKnown) != 0 {
				return errors.New("end does not match reviewed empty removal")
			}
			ended = true
		default:
			return fmt.Errorf("unsupported historical evidence %q", ev.Kind)
		}
	}
	if !ended || !aborted || p.active != nil || !p.boundary() || pending != "" {
		return errors.New("incomplete removal proof")
	}
	return nil
}
