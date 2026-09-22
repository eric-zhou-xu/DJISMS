package receive

import (
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestLegacyEmptyRemovalRequiresCompleteImmutableFacts(t *testing.T) {
	var events []ReviewEvent
	add := func(k string, v any) {
		b, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		events = append(events, ReviewEvent{k, b})
	}
	bodies := []string{"+CMGF: 0", `+CPMS: "ME",0,23,"ME",0,23,"ME",0,23`, "+CNMI: 2,1,0,0,0", "+CSMS: 0,1,1,1", "", `+CPMS: "ME",0,23,"ME",0,23,"ME",0,23`}
	for i, body := range bodies {
		cmd := Command{Kind: uint8(i + 1)}
		wire, _ := cmd.wire()
		add("out_intent", map[string]any{"command": wire})
		add("out_succeeded", cmd)
		raw := []byte(wire + "\r\r\n" + body + "\r\nOK\r\n")
		add("usb_read", map[string]any{"valid_hex": hex.EncodeToString(raw), "diagnostic": classifyRead(0, uint32(len(raw)), 512)})
		add("response", map[string]any{})
		if i == 4 {
			add("snapshot_raw", []any{})
		}
	}
	add("usb_read", map[string]any{"valid_hex": "", "diagnostic": classifyRead(0xe00002ed, 512, 512)})
	add("session_end", Summary{Error: LegacyRemovalError})
	if e := ReviewLegacyEmptyRemoval(events); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"missing_start", "missing_end", "unmatched_write", "nonempty_snapshot", "partial_before_error", "activity_after_error", "wrong_diagnostic", "wrong_end", "read_length_invalid"} {
		t.Run(name, func(t *testing.T) {
			copyEvents := append([]ReviewEvent{}, events...)
			switch name {
			case "missing_start":
				copyEvents = copyEvents[1:]
			case "missing_end":
				copyEvents = copyEvents[:len(copyEvents)-1]
			case "unmatched_write":
				copyEvents[1].Data = []byte(`{"kind":7,"index":0}`)
			case "nonempty_snapshot":
				for i := range copyEvents {
					if copyEvents[i].Kind == "snapshot_raw" {
						copyEvents[i].Data = []byte(`[{}]`)
					}
				}
			case "partial_before_error":
				raw := []byte("+CMTI:")
				b, _ := json.Marshal(map[string]any{"valid_hex": hex.EncodeToString(raw), "diagnostic": classifyRead(0, uint32(len(raw)), 512)})
				n := len(copyEvents) - 2
				copyEvents = append(copyEvents[:n], append([]ReviewEvent{{"usb_read", b}}, copyEvents[n:]...)...)
			case "activity_after_error":
				copyEvents = append(copyEvents, events[0])
			case "wrong_diagnostic":
				copyEvents[len(copyEvents)-2].Data = []byte(`{"diagnostic":{"iokit_return_code":3758097084}}`)
			case "wrong_end":
				copyEvents[len(copyEvents)-1].Data = []byte(`{}`)
			case "read_length_invalid":
				copyEvents[2].Data = []byte(`{"valid_hex":"00","diagnostic":{"iokit_return_code":0}}`)
			}
			if e := ReviewLegacyEmptyRemoval(copyEvents); e == nil {
				t.Fatal("unsafe review accepted")
			}
		})
	}
}
