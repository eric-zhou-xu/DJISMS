package receive

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// ReviewClosedEmpty replays the fixed read-only plan and its final settings.
// Any arrival, missing fact, write, unfinished frame or nonempty inventory fails.
func ReviewClosedEmpty(events []ReviewEvent) error {
	p := &framer{when: "closed-receive-review"}
	plan := []uint8{1, 2, 3, 4, 5, 6, 8, 9, 10, 11}
	commands, responses := 0, 0
	pending := ""
	empty, ended := false, false
	results := []Response{}
	for _, ev := range events {
		if ended {
			return errors.New("fact after session end")
		}
		switch ev.Kind {
		case "out_intent":
			if pending != "" || commands != responses || commands >= len(plan) {
				return errors.New("unexpected OUT")
			}
			var v struct {
				Command string
				Hex     string
			}
			if e := json.Unmarshal(ev.Data, &v); e != nil {
				return e
			}
			wire, _ := (Command{Kind: plan[commands]}).wire()
			if v.Command != wire || v.Hex != hex.EncodeToString([]byte(wire+"\r")) {
				return errors.New("unreviewed command bytes")
			}
			pending = wire
		case "out_succeeded":
			var cmd Command
			if e := json.Unmarshal(ev.Data, &cmd); e != nil {
				return e
			}
			wire, e := cmd.wire()
			if e != nil || pending == "" || wire != pending || commands >= len(plan) || cmd.Kind != plan[commands] {
				return errors.New("unmatched OUT")
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
			if d.ReturnCode != 0 {
				return errors.New("non-successful read in closed proof")
			}
			b, e := hex.DecodeString(v.Hex)
			if e != nil {
				return e
			}
			if !d.CountValid || d.ActualBytes == nil || *d.ActualBytes != d.RawSize || int(d.RawSize) != len(b) || len(b) > 512 {
				return errors.New("invalid read extent")
			}
			if e = p.feed(b); e != nil {
				return e
			}
			if len(p.notices) != 0 || len(p.directs) != 0 {
				return errors.New("arrival in empty session")
			}
		case "response":
			r, e := p.finish()
			if e != nil {
				return e
			}
			var recorded Response
			if e = json.Unmarshal(ev.Data, &recorded); e != nil {
				return e
			}
			// nil and empty message slices encode identically with omitempty.
			if len(r.Messages) != 0 || len(recorded.Messages) != 0 {
				return errors.New("nonempty raw response")
			}
			r.Messages = nil
			recorded.Messages = nil
			if !reflect.DeepEqual(r, recorded) {
				return errors.New("response differs from raw replay")
			}
			results = append(results, r)
			responses++
			if responses == 4 {
				n, e := settings(results)
				if e != nil || n != 0 {
					return errors.New("initial settings/inventory")
				}
			}
			if responses == 6 || responses == 7 {
				n, e := storage(r)
				if e != nil || n != 0 {
					return errors.New("nonempty inventory")
				}
			}
		case "snapshot_raw":
			var raw []json.RawMessage
			if e := json.Unmarshal(ev.Data, &raw); e != nil {
				return e
			}
			if string(ev.Data) == "null" || len(raw) != 0 || responses != 5 || empty {
				return errors.New("invalid empty snapshot")
			}
			empty = true
		case "idle_timeout_summary":
			if pending != "" || p.active != nil || !p.boundary() || (responses != 6 && responses != 10) {
				return errors.New("idle at unsafe boundary")
			}
			var v struct {
				Valid   bool `json:"count_is_not_actual_bytes"`
				Samples int  `json:"samples"`
			}
			if e := json.Unmarshal(ev.Data, &v); e != nil {
				return e
			}
			if !v.Valid || v.Samples <= 0 {
				return errors.New("invalid timeout summary")
			}
		case "session_end":
			var end Summary
			var fields map[string]json.RawMessage
			if e := json.Unmarshal(ev.Data, &end); e != nil {
				return e
			}
			if e := json.Unmarshal(ev.Data, &fields); e != nil {
				return e
			}
			for _, k := range []string{"closed", "settings_reconciled", "initial_used", "final_used", "read_indices", "known_index_notifications", "recoverable_idle_interruption"} {
				if _, ok := fields[k]; !ok {
					return fmt.Errorf("missing %s", k)
				}
			}
			if _, ok := fields["delete_attempted"]; ok {
				return errors.New("destructive session")
			}
			if !end.Closed || !end.Reconciled || end.Error != "" || end.Interrupted || end.RemovedClosure || end.InitialUsed != 0 || end.FinalUsed != 0 || len(end.ReadIndices) != 0 || len(end.IgnoredKnown) != 0 || responses != 10 || !empty {
				return errors.New("incomplete clean empty closure")
			}
			n, e := settings([]Response{results[7], results[6], results[8], results[9]})
			if e != nil || n != 0 {
				return errors.New("final settings differ")
			}
			ended = true
		default:
			return fmt.Errorf("unsupported receive fact %q", ev.Kind)
		}
	}
	if !ended || commands != 10 || responses != 10 || pending != "" || p.active != nil || !p.boundary() {
		return errors.New("incomplete closed receive proof")
	}
	return nil
}
