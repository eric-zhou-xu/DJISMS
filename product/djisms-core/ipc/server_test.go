package ipc

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestTypedBoundary(t *testing.T) {
	for _, raw := range []string{`{"version":1,"id":"x","method":"raw_at","command":"AT+CMGD=1,4"}`, `{"version":1,"id":"x","method":"status","path":"/tmp/raw"}`, `{"version":2,"id":"x","method":"status"}`, `{"version":1,"id":"x","method":"messages","limit":201}`, `{"version":1,"id":"x","method":"status"}{}`, strings.Repeat(" ", 17000)} {
		if _, e := Parse([]byte(raw)); e == nil {
			t.Fatal("accepted", raw)
		}
	}
	if _, e := Parse([]byte(`{"version":1,"id":"x","method":"messages","search":"中文","limit":100}`)); e != nil {
		t.Fatal(e)
	}
}
func FuzzTypedRequest(f *testing.F) {
	f.Add(`{"version":1,"id":"x","method":"status"}`)
	f.Fuzz(func(t *testing.T, raw string) { _, _ = Parse([]byte(raw)) })
}

func TestPowerLifecycleBoundary(t *testing.T) {
	for _, state := range []string{"prepare_sleep", "awake"} {
		if _, e := Parse([]byte(`{"version":1,"id":"x","method":"power","power":"` + state + `"}`)); e != nil {
			t.Fatal(e)
		}
	}
	for _, raw := range []string{`{"version":1,"id":"x","method":"power","power":"reset"}`, `{"version":1,"id":"x","method":"status","power":"awake"}`, `{"version":1,"id":"x","method":"power"}`} {
		if _, e := Parse([]byte(raw)); e == nil {
			t.Fatal("unexpected power command accepted")
		}
	}
}

func TestInitializationAnnouncedBeforeArchiveFailure(t *testing.T) {
	var out strings.Builder
	// A regular file cannot be an archive directory; no hardware is acquired.
	root := t.TempDir() + "/file"
	if err := os.WriteFile(root, []byte("preserved"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(root, &out); err == nil {
		t.Fatal("invalid archive accepted")
	}
	var event Response
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &event); err != nil {
		t.Fatal(err)
	}
	if event.Event != "initializing" || event.Version != Version {
		t.Fatalf("unexpected startup event: %+v", event)
	}
}
