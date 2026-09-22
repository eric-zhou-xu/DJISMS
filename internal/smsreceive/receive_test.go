package smsreceive

import (
	"context"
	"encoding/hex"
	"errors"
	"github.com/iniwex5/vohive/internal/safeusb"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type input struct {
	data    string
	code    uint32
	invalid bool
}
type fakeBackend struct {
	inputs               []input
	writes               []Query
	opens, closes, reads int
	writeFail            bool
	snap                 safeusb.Snapshot
	onWrite              func(Query)
}

func fixture() safeusb.Snapshot {
	raw, _ := hex.DecodeString(reviewedHex)
	d := safeusb.Device{Vendor: safeusb.VendorID, Product: safeusb.ProductID, BCDDevice: 792, LocationID: reviewedLocation}
	if e := safeusb.ParseConfiguration(raw, &d); e != nil {
		panic(e)
	}
	return safeusb.Snapshot{Schema: 1, Devices: []safeusb.Device{d}}
}
func (f *fakeBackend) snapshot() (safeusb.Snapshot, error) {
	if f.snap.Schema != 0 {
		return f.snap, nil
	}
	return fixture(), nil
}
func (f *fakeBackend) open() error  { f.opens++; return nil }
func (f *fakeBackend) close() error { f.closes++; return nil }
func (f *fakeBackend) write(q Query, d time.Duration) error {
	if d <= 0 || d > 250*time.Millisecond {
		panic("unbounded write")
	}
	f.writes = append(f.writes, q)
	if f.onWrite != nil {
		f.onWrite(q)
	}
	if f.writeFail {
		return errors.New("write failed")
	}
	return nil
}
func (f *fakeBackend) readDiagnostic(b []byte, d time.Duration) (ReadDiagnostic, error) {
	if d <= 0 || d > 200*time.Millisecond {
		panic("unbounded read")
	}
	f.reads++
	x := input{code: usbTransactionTimeout}
	if len(f.inputs) > 0 {
		x = f.inputs[0]
		f.inputs = f.inputs[1:]
	}
	n := copy(b, x.data)
	if x.invalid {
		return classifyRead(0, uint32(len(b)+1), uint32(len(b))), nil
	}
	if x.code != 0 {
		return classifyRead(x.code, uint32(len(b)), uint32(len(b))), errors.New("read error")
	}
	return classifyRead(0, uint32(n), uint32(len(b))), nil
}

var bodies = []string{"+CMGF: 0", `+CPMS: "ME",1,23,"ME",1,23,"ME",1,23`, "+CNMI: 2,1,0,0,0", "+CMGL: 1,0,,1\r\n0000", `+CPMS: "ME",1,23,"ME",1,23,"ME",1,23`, "+CMGF: 0", "+CNMI: 2,1,0,0,0"}

func script() []input {
	var out []input
	for i, d := range definitions {
		out = append(out, input{code: usbTransactionTimeout}, input{data: d.command + "\r\r\n" + bodies[i] + "\r\nOK\r\n"})
	}
	return out
}
func connect(t *testing.T, f *fakeBackend) *Transport {
	t.Helper()
	p := &Transport{b: f}
	if e := p.Connect(context.Background()); e != nil {
		t.Fatal(e)
	}
	return p
}
func lossless(t *testing.T, r Report) {
	t.Helper()
	var raw, ev []byte
	for _, x := range r.Reads {
		if x.StreamOffset != len(raw) {
			t.Fatal("read offset")
		}
		b, e := hex.DecodeString(x.ValidHex)
		if e != nil {
			t.Fatal(e)
		}
		raw = append(raw, b...)
	}
	for _, x := range r.Events {
		if x.Offset != len(ev) {
			t.Fatal("event offset")
		}
		b, _ := hex.DecodeString(x.Hex)
		ev = append(ev, b...)
	}
	if string(raw) != string(ev) {
		t.Fatalf("evidence missing %x / %x", raw, ev)
	}
}
func TestFixedPlanAndForensicNoteNonblocking(t *testing.T) {
	f := &fakeBackend{inputs: script()}
	p := connect(t, f)
	saved := map[string]bool{}
	f.onWrite = func(q Query) {
		if !saved[definitions[q-1].command] {
			t.Fatal("not preserved before OUT")
		}
	}
	r, e := p.ExecutePlan(context.Background(), func(a Report) error {
		if a.SafeToSend && !a.WriteAttempted && len(a.Reads) > 0 {
			saved[a.Command] = true
		}
		return nil
	})
	if e != nil || r.EngineeringOutcome != "PASS" || r.ForensicAttribution != "INCONCLUSIVE" || f.closes != 1 || f.opens != 1 || len(f.writes) != 7 {
		t.Fatal(e, r)
	}
	for i, a := range r.Queries {
		if !a.Success || a.Command != definitions[i].command || (a.Command != "AT+CMGL=4" && len(a.ResponseLines) == 0) || a.ResponseEvidenceSufficient {
			t.Fatal(a)
		}
		lossless(t, a)
	}
	if _, e = p.ExecutePlan(context.Background(), func(Report) error { return nil }); e == nil || len(f.writes) != 7 {
		t.Fatal("repeated plan")
	}
	_ = p.Close()
	if f.closes != 1 {
		t.Fatal("duplicate close")
	}
}
func TestEveryResponseSplit(t *testing.T) {
	for q := Query(1); q <= 7; q++ {
		wire := definitions[q-1].command + "\r\r\n" + bodies[q-1] + "\r\nOK\r\n"
		for cut := 1; cut < len(wire); cut++ {
			p := newATStream(1, q)
			_ = p.markWritten()
			if e := p.feed([]byte(wire[:cut])); e != nil {
				t.Fatal(q, cut, e)
			}
			if e := p.feed([]byte(wire[cut:])); e != nil {
				t.Fatal(q, cut, e)
			}
			if e := p.validateSuccess(); e != nil {
				t.Fatal(q, cut, e)
			}
			if e := validateResponse(q, p.payload); e != nil {
				t.Fatal(e)
			}
		}
	}
}
func TestAmbiguityAndFailuresStopPlanWithoutRetry(t *testing.T) {
	for _, bad := range []input{{data: "AT+CMGF?\r\nOK\r\n"}, {data: "AT\r\n+CMGF: 0\r\nOK\r\n"}, {data: "AT+CMGF?\r\n+X: \"+CMGF: 0\"\r\nOK\r\n"}, {data: "AT+CMGF?\r\n+CMGF: 0\r\nOK\r\nTAIL"}, {data: "AT+CMGF?\r\n+CMT: 1\r\n+CMGF: 0\r\nOK\r\n"}, {data: "AT+CMGF?\r\n+CMGF: 0\r\nERROR\r\n"}, {data: "AT+CMGF?\n+CMGF: 0\nOK\n"}, {data: "AT+CMGF?\r\n+CMGF: 0\r\nOK\r\n", code: usbTransactionTimeout}, {invalid: true}} {
		f := &fakeBackend{inputs: []input{{code: usbTransactionTimeout}, bad}}
		p := connect(t, f)
		r, e := p.ExecutePlan(context.Background(), func(Report) error { return nil })
		if e == nil || r.EngineeringOutcome == "PASS" || len(f.writes) != 1 || f.closes != 1 {
			t.Fatal(e, r)
		}
		lossless(t, r.Queries[0])
	}
}
func TestRealPreFragmentNotErased(t *testing.T) {
	f := &fakeBackend{inputs: []input{{data: "+"}, {data: "X"}, {data: " "}, {data: "tail"}, {data: "\r\nAT+CMGF?\r\n+CMGF: 0\r\nOK\r\n"}}}
	p := connect(t, f)
	r, e := p.ExecutePlan(context.Background(), func(Report) error { return nil })
	if e == nil || len(f.writes) != 1 || r.Queries[0].EchoMatched {
		t.Fatal(e, r)
	}
	lossless(t, r.Queries[0])
}
func TestRecorderAndCancellation(t *testing.T) {
	for _, stage := range []string{"nil", "pre", "after", "write", "cancel"} {
		f := &fakeBackend{inputs: script()}
		p := connect(t, f)
		ctx, cancel := context.WithCancel(context.Background())
		record := func(a Report) error {
			if stage == "pre" || (stage == "after" && a.WriteSucceeded) {
				return errors.New("disk full")
			}
			return nil
		}
		if stage == "nil" {
			record = nil
		}
		if stage == "write" {
			f.writeFail = true
		}
		if stage == "cancel" {
			cancel()
		}
		r, e := p.ExecutePlan(ctx, record)
		cancel()
		if e == nil || r.EngineeringOutcome == "PASS" || f.closes != 1 {
			t.Fatal(stage, e, r)
		}
		want := 0
		if stage == "after" || stage == "write" {
			want = 1
		}
		if len(f.writes) != want {
			t.Fatal(stage, "writes", f.writes)
		}
	}
}
func TestSettingsSchemas(t *testing.T) {
	for _, x := range []struct {
		q    Query
		line string
	}{{MessageFormat, "+CMGF: 2"}, {Storage, `+CPMS: "ME",5,2,"ME",0,255,"ME",0,255`}, {Storage, `+CPMS: 0,255`}, {Notifications, "+CNMI: 2,1,0,0,2"}} {
		if validateResponse(x.q, []string{x.line}) == nil {
			t.Fatal(x)
		}
	}
}
func TestProfileAndNativeAllowlist(t *testing.T) {
	s := fixture()
	s.Devices[0].Interfaces[2].Alternate = 1
	if checkSnapshot(s) == nil {
		t.Fatal("alternate")
	}
	for i := 0; i < 256; i++ {
		_, e := Query(i).command()
		if (e == nil) != (i >= 1 && i <= 7) {
			t.Fatal(i)
		}
	}
	b, e := os.ReadFile("native.h")
	if e != nil {
		t.Fatal(e)
	}
	text := string(b)
	allowed := map[string]bool{}
	for _, m := range strings.Fields("Release QueryInterface USBInterfaceClose GetNumberOfConfigurations GetConfigurationDescriptorPtr GetInterfaceNumber GetAlternateSetting GetConfigurationValue GetInterfaceClass GetInterfaceSubClass GetInterfaceProtocol GetNumEndpoints GetPipeProperties USBInterfaceOpen WritePipeTO ReadPipeTO") {
		allowed[m] = true
	}
	for _, m := range regexp.MustCompile(`->([A-Za-z0-9_]+)\(`).FindAllStringSubmatch(text, -1) {
		if !allowed[m[1]] {
			t.Fatal(m)
		}
	}
	wire := regexp.MustCompile(`wire="([^"]+)"`).FindAllStringSubmatch(text, -1)
	var got, want []string
	for _, m := range wire {
		got = append(got, m[1])
	}
	for _, d := range definitions {
		want = append(want, d.command+`\r`)
	}
	if !reflect.DeepEqual(got, want) || strings.Count(text, "->WritePipeTO(") != 1 || strings.Count(text, "->USBInterfaceOpen(") != 1 {
		t.Fatal(got, want)
	}
}
func TestConcurrentPlanCannotDoubleSend(t *testing.T) {
	f := &fakeBackend{inputs: script()}
	p := connect(t, f)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = p.ExecutePlan(context.Background(), func(Report) error { return nil }) }()
	}
	wg.Wait()
	if len(f.writes) != 7 || f.closes != 1 {
		t.Fatal(f)
	}
}
func FuzzReadOnlyResponseCannotSkipEcho(f *testing.F) {
	for _, s := range []string{"OK", "AT+CMGF?", "+CMGF: 0", "+CMT: 1", "\x00", "\r\n"} {
		f.Add([]byte(s), uint8(3))
	}
	f.Fuzz(func(t *testing.T, b []byte, step uint8) {
		if len(b) > 512 {
			return
		}
		p := newATStream(1, MessageFormat)
		_ = p.markWritten()
		wire := []byte("+UNSEEN: " + hex.EncodeToString(b) + "\r\n+CMGF: 0\r\nOK\r\n")
		n := int(step)%32 + 1
		for i := 0; i < len(wire); i += n {
			end := i + n
			if end > len(wire) {
				end = len(wire)
			}
			if p.feed(wire[i:end]) != nil {
				break
			}
		}
		if p.complete() {
			t.Fatal("URC supplied echo")
		}
	})
}

func TestListMalformedAndMissingFrames(t *testing.T) {
	for _, body := range []string{"+CMGL: 1,0,,1\r\nOK", "+CMGL: 1,0,,1\r\nGG", "+CMGL: 1,0,,1\r\n0", "+CMGL: 1,0,,1\r\n0000\r\n+CMGL: 1,0,,1\r\n0000", "+CMGL: 1,9,,1\r\n0000", "OK\r\n+CMGL: 1,0,,1\r\n0000", "+CMT: 1\r\n0000"} {
		in := script()
		in[7].data = "AT+CMGL=4\r\n" + body + "\r\nOK\r\n"
		f := &fakeBackend{inputs: in}
		p := connect(t, f)
		r, e := p.ExecutePlan(context.Background(), func(Report) error { return nil })
		if e == nil || r.EngineeringOutcome == "PASS" || len(f.writes) != 4 {
			t.Fatal(body, e, r)
		}
		for _, q := range r.Queries {
			lossless(t, q)
		}
	}
}
func TestPrerequisitesAndCountReconciliation(t *testing.T) {
	for _, which := range []int{0, 1, 2, 4} {
		in := script()
		switch which {
		case 0:
			in[1].data = "AT+CMGF?\r\n+CMGF: 1\r\nOK\r\n"
		case 1:
			in[3].data = strings.ReplaceAll(in[3].data, "ME", "SM")
		case 2:
			in[5].data = strings.ReplaceAll(in[5].data, "2,1,0,0,0", "1,1,0,0,0")
		case 4:
			in[9].data = strings.ReplaceAll(in[9].data, ",1,23", ",2,23")
		}
		f := &fakeBackend{inputs: in}
		r, e := connect(t, f).ExecutePlan(context.Background(), func(Report) error { return nil })
		want := 3
		if which == 4 {
			want = 7
		}
		if e == nil || len(f.writes) != want || r.EngineeringOutcome == "PASS" {
			t.Fatal(which, e, r)
		}
		if which == 4 && len(r.Queries[3].Messages) != 1 {
			t.Fatal("lost raw message")
		}
	}
}
func TestEmptyStore(t *testing.T) {
	in := script()
	in[3].data = strings.ReplaceAll(in[3].data, ",1,23", ",0,23")
	in[9].data = strings.ReplaceAll(in[9].data, ",1,23", ",0,23")
	in[7].data = "AT+CMGL=4\r\nOK\r\n"
	r, e := connect(t, &fakeBackend{inputs: in}).ExecutePlan(context.Background(), func(Report) error { return nil })
	if e != nil || len(r.Queries[3].Messages) != 0 {
		t.Fatal(e, r)
	}
}
