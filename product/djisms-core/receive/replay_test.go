package receive

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestObservedDirectEventEverySplit(t *testing.T) {
	path := os.Getenv("SMSLIVE_REPLAY")
	if path == "" {
		t.Skip("no private observed-event archive supplied")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var a struct {
		Wire   string `json:"raw_wire_hex"`
		PDU    string `json:"raw_pdu"`
		Header string `json:"raw_header"`
	}
	if e = json.Unmarshal(b, &a); e != nil {
		t.Fatal(e)
	}
	wire, e := hex.DecodeString(a.Wire)
	if e != nil {
		t.Fatal(e)
	}
	for cut := 1; cut < len(wire); cut++ {
		p := &framer{}
		if e = p.feed(wire[:cut]); e != nil {
			t.Fatal(cut, e)
		}
		if e = p.feed(wire[cut:]); e != nil {
			t.Fatal(cut, e)
		}
		if !p.boundary() || len(p.directs) != 1 || p.directs[0].PDU != a.PDU || p.directs[0].Header != a.Header {
			t.Fatal("replay changed original frame")
		}
	}
}
