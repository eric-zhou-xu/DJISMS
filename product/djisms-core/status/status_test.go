package status

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
	d := safeusb.Device{Vendor: safeusb.VendorID, Product: safeusb.ProductID, BCDDevice: 792, LocationID: uint32(0)}
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

var bodies = []string{"Quectel", "EG25-G", "EG25GGBR07A08M2G", "+CPIN: READY", "+CSQ: 20,99", "+CREG: 0,1", "+CEREG: 0,1", "+COPS: 0,0,\"CHN-UNICOM\",7", "+QCCID: 89860000000000000001", "+CNUM: ,\"+8613800000000\",145"}

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
	if e != nil || r.EngineeringOutcome != "PASS" || r.ForensicAttribution != "INCONCLUSIVE" || f.closes != 1 || f.opens != 1 || len(f.writes) != 10 {
		t.Fatal(e, r)
	}
	for i, a := range r.Queries {
		if !a.Success || a.Command != definitions[i].command || len(a.ResponseLines) == 0 || a.ResponseEvidenceSufficient {
			t.Fatal(a)
		}
		lossless(t, a)
	}
	if _, e = p.ExecutePlan(context.Background(), func(Report) error { return nil }); e == nil || len(f.writes) != 10 {
		t.Fatal("repeated plan")
	}
	_ = p.Close()
	if f.closes != 1 {
		t.Fatal("duplicate close")
	}
}
func TestEveryResponseSplit(t *testing.T) {
	for q := Query(1); q <= 10; q++ {
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
	for _, bad := range []input{{data: "AT+CGMI\r\nOK\r\n"}, {data: "AT\r\nQuectel\r\nOK\r\n"}, {data: "AT+CGMI\r\n+X: \"Quectel\"\r\nOK\r\n"}, {data: "AT+CGMI\r\nQuectel\r\nOK\r\nTAIL"}, {data: "AT+CGMI\r\n+CMT: 1\r\nQuectel\r\nOK\r\n"}, {data: "AT+CGMI\r\nQuectel\r\nERROR\r\n"}, {data: "AT+CGMI\nQuectel\nOK\n"}, {data: "AT+CGMI\r\nQuectel\r\nOK\r\n", code: usbTransactionTimeout}, {invalid: true}} {
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
	f := &fakeBackend{inputs: []input{{data: "+"}, {data: "X"}, {data: " "}, {data: "tail"}, {code: usbTransactionTimeout}}}
	p := connect(t, f)
	r, e := p.ExecutePlan(context.Background(), func(Report) error { return nil })
	if e == nil || len(f.writes) != 0 || r.Queries[0].EchoMatched {
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
func TestResponseSchemasRejectURCAndDuplicates(t *testing.T) {
	for _, x := range []struct {
		q     Query
		lines []string
	}{{Registration, []string{`+CREG: 1,"1","2"`}}, {SignalQuality, []string{"+CSQ: -1,99"}}, {PINState, []string{"+CPIN:"}}, {Operator, []string{"+COPS: abc"}}, {EPSRegistration, []string{"+CEREG: 0,1", "+CEREG: 0,5"}}} {
		if validateResponse(x.q, x.lines) == nil {
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
		if (e == nil) != (i >= 1 && i <= 10) {
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
	if len(f.writes) != 10 || f.closes != 1 {
		t.Fatal(f)
	}
}
func FuzzReadOnlyResponseCannotSkipEcho(f *testing.F) {
	for _, s := range []string{"OK", "AT+CGMI", "Quectel", "+CMT: 1", "\x00", "\r\n"} {
		f.Add([]byte(s), uint8(3))
	}
	f.Fuzz(func(t *testing.T, b []byte, step uint8) {
		if len(b) > 512 {
			return
		}
		p := newATStream(1, Manufacturer)
		_ = p.markWritten()
		wire := []byte("+UNSEEN: " + hex.EncodeToString(b) + "\r\nQuectel\r\nOK\r\n")
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
