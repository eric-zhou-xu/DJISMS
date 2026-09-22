package status

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

const directWire = "+CMT: ,2\r\n001034\r\n"

func TestInterleavingAtEveryBoundaryAndEveryByteSplit(t *testing.T) {
	for _, q := range []Query{Manufacturer, PINState, Registration, SIMCardID} {
		cmd, _ := q.command()
		body := bodies[int(q)-1]
		pieces := []string{cmd + "\r\r\n", body + "\r\n", "OK\r\n"}
		for place := 0; place <= len(pieces); place++ {
			wire := strings.Join(pieces[:place], "") + directWire + strings.Join(pieces[place:], "")
			for cut := 1; cut < len(wire); cut++ {
				p := newATStream(7, q)
				if e := p.markWritten(); e != nil {
					t.Fatal(e)
				}
				if e := p.feed([]byte(wire[:cut])); e != nil {
					t.Fatal(q, place, cut, e)
				}
				if e := p.feed([]byte(wire[cut:])); e != nil {
					t.Fatal(q, place, cut, e)
				}
				if e := p.validateSuccess(); e != nil {
					t.Fatal(q, place, cut, e)
				}
				if len(p.frames) != 1 || p.frames[0].PDU != "001034" || len(p.payload) != 1 || p.payload[0] != body {
					t.Fatal("wrong attribution", q, place, cut)
				}
				for _, ev := range p.events {
					if strings.HasPrefix(ev.Kind, "asynchronous") && ev.RequestID != 0 {
						t.Fatal("SMS attributed to command")
					}
				}
			}
		}
	}
}
func TestKnownURCsCannotBecomeIdentityBody(t *testing.T) {
	p := newATStream(1, Manufacturer)
	p.markWritten()
	wire := "AT+CGMI\r\r\nRING\r\n+QIND: SMS DONE\r\n+CMTI: \"ME\",0\r\nQuectel\r\n+CREG: 1,\"A0\",\"B1\",7\r\nOK\r\n"
	if e := p.feed([]byte(wire)); e != nil {
		t.Fatal(e)
	}
	if e := p.validateSuccess(); e != nil {
		t.Fatal(e)
	}
	if len(p.payload) != 1 || p.payload[0] != "Quectel" {
		t.Fatal(p.payload)
	}
}
func TestInterleavingRejectsAmbiguousOrMalformedFrames(t *testing.T) {
	for _, body := range []string{"+CMT: ,2\r\nOK\r\n", "+CMT: ,2\r\nAT+CGMI\r\n", "+CMT: ,2\r\n0012\r\n", "+CMT: ,2\r\n001234\r\n", "+CMT: ,2\r\n+CMT: ,2\r\n001034\r\n", "+UNKNOWN: a\r\n", "+CMT: \"sender\",\"timestamp\"\r\ntext\r\n"} {
		p := newATStream(1, Manufacturer)
		p.markWritten()
		e := p.feed([]byte("AT+CGMI\r\n" + body + "Quectel\r\nOK\r\n"))
		if e == nil && p.complete() {
			t.Fatal("unsafe accepted", body)
		}
	}
}
func TestTrailingFragmentFinishesBeforeNextQuery(t *testing.T) {
	f := &fakeBackend{inputs: script()}
	f.inputs[1].data += "+CMT: ,2\r\n001"
	f.inputs = append(f.inputs[:2], append([]input{{data: "034\r\n"}}, f.inputs[2:]...)...)
	tr := connect(t, f)
	r, e := tr.ExecutePlan(context.Background(), func(Report) error { return nil })
	if e != nil || len(r.Queries[0].Frames) != 1 || len(f.writes) != 10 {
		t.Fatal(e, r)
	}
}
func TestReadOnlyContinuationReplaysEveryPrefixWithoutSending(t *testing.T) {
	wire := directWire + "AT+CGMI\r\r\nQuectel\r\nOK\r\n"
	for cut := 0; cut < len(wire); cut++ {
		t.Run(fmt.Sprint(cut), func(t *testing.T) {
			seed := Report{RequestID: 1, Interface: 2, Command: "AT+CGMI", PlannedOutHex: hex.EncodeToString([]byte("AT+CGMI\r")), WriteAttempted: true, WriteSucceeded: true}
			if cut > 0 {
				seed.Reads = []ReadEvidence{{Index: 1, Phase: "after_write", Diagnostic: classifyRead(0, uint32(cut), 512), ValidHex: hex.EncodeToString([]byte(wire[:cut]))}}
			}
			f := &fakeBackend{inputs: []input{{data: wire[cut:]}}}
			tr := connect(t, f)
			r, e := tr.ContinueRead(context.Background(), seed, func(Report) error { return nil })
			if e != nil || len(f.writes) != 0 || !r.CloseSucceeded || len(r.Queries) != 1 || len(r.Queries[0].Frames) != 1 {
				t.Fatal(e, r, f.writes)
			}
		})
	}
}

func TestUncertainWriteIntentContinuationNeverResends(t *testing.T) {
	seed := Report{RequestID: 3, Interface: 2, Command: "AT+CGMI", PlannedOutHex: hex.EncodeToString([]byte("AT+CGMI\r")), WriteAttempted: true, WriteSucceeded: false}
	f := &fakeBackend{inputs: []input{{data: directWire + "AT+CGMI\r\nQuectel\r\nOK\r\n"}}}
	tr := connect(t, f)
	r, e := tr.ContinueRead(context.Background(), seed, func(Report) error { return nil })
	if e != nil || len(f.writes) != 0 || !r.Queries[0].Success || r.Queries[0].WriteSucceeded {
		t.Fatal("rewrote original OUT outcome", e, r)
	}
}
func TestUnknownPrintableLineNotIdentityResponse(t *testing.T) {
	for _, q := range []Query{Manufacturer, Model, Revision} {
		p := newATStream(1, q)
		p.markWritten()
		cmd, _ := q.command()
		e := p.feed([]byte(cmd + "\r\nUNKNOWN VENDOR EVENT\r\n" + bodies[int(q)-1] + "\r\nOK\r\n"))
		if e == nil {
			t.Fatal("unknown text silently accepted")
		}
	}
}
