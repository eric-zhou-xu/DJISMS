package receive

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

func closedFacts(t *testing.T) []ReviewEvent {
	t.Helper()
	b, e := os.ReadFile("testdata/closed-empty-session.json")
	if e != nil {
		t.Fatal(e)
	}
	var f []struct {
		Kind string
		Data json.RawMessage
	}
	if e = json.Unmarshal(b, &f); e != nil {
		t.Fatal(e)
	}
	out := []ReviewEvent{}
	for _, v := range f {
		out = append(out, ReviewEvent{v.Kind, v.Data})
	}
	return out
}
func TestClosedReceiveRawProof(t *testing.T) {
	facts := closedFacts(t)
	if e := ReviewClosedEmpty(facts); e != nil {
		t.Fatal(e)
	}
	for i, f := range facts {
		if f.Kind == "idle_timeout_summary" {
			continue
		} // Aggregated idle counts are not bytes.
		t.Run(fmt.Sprintf("missing_%d_%s", i, f.Kind), func(t *testing.T) {
			a := append([]ReviewEvent{}, facts[:i]...)
			a = append(a, facts[i+1:]...)
			if e := ReviewClosedEmpty(a); e == nil {
				t.Fatal("missing required fact accepted")
			}
		})
	}
	for _, field := range []string{"closed", "settings_reconciled", "initial_used", "final_used", "read_indices", "known_index_notifications", "recoverable_idle_interruption"} {
		t.Run("absent_"+field, func(t *testing.T) {
			a := closedFacts(t)
			var end map[string]any
			json.Unmarshal(a[len(a)-1].Data, &end)
			delete(end, field)
			a[len(a)-1].Data, _ = json.Marshal(end)
			if e := ReviewClosedEmpty(a); e == nil {
				t.Fatal("incomplete summary accepted")
			}
		})
	}
	for _, mode := range []string{"delete", "unknown", "after_end", "nonempty", "settings", "response_mutation", "arrival"} {
		t.Run(mode, func(t *testing.T) {
			a := closedFacts(t)
			switch mode {
			case "delete", "unknown":
				a[3].Kind = mode
			case "after_end":
				a = append(a, a[0])
			case "nonempty":
				for i := range a {
					if a[i].Kind == "snapshot_raw" {
						a[i].Data = []byte(`[{}]`)
					}
				}
			case "settings":
				for i := range a {
					if a[i].Kind == "session_end" {
						var m map[string]any
						json.Unmarshal(a[i].Data, &m)
						m["settings_reconciled"] = false
						a[i].Data, _ = json.Marshal(m)
					}
				}
			case "response_mutation":
				for i := range a {
					if a[i].Kind == "response" {
						a[i].Data = []byte(`{}`)
						break
					}
				}
			case "arrival":
				a = append(a[:len(a)-1], ReviewEvent{"notice", []byte(`{}`)}, a[len(a)-1])
			}
			if e := ReviewClosedEmpty(a); e == nil {
				t.Fatal("unsafe proof accepted")
			}
		})
	}
}
