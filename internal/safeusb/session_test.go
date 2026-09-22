package safeusb

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeTransport struct {
	calls                          []string
	reply                          string
	claimErr, queryErr, releaseErr error
	inside, maxInside              int
	mu                             sync.Mutex
}

func (f *fakeTransport) Claim(_ context.Context, iface, in, out uint8) error {
	f.calls = append(f.calls, "claim")
	return f.claimErr
}
func (f *fakeTransport) Query(_ context.Context, cmd string) (string, error) {
	f.mu.Lock()
	f.inside++
	if f.inside > f.maxInside {
		f.maxInside = f.inside
	}
	f.calls = append(f.calls, cmd)
	f.mu.Unlock()
	time.Sleep(time.Millisecond)
	f.mu.Lock()
	f.inside--
	f.mu.Unlock()
	return f.reply, f.queryErr
}
func (f *fakeTransport) Release(uint8) error {
	f.calls = append(f.calls, "release")
	return f.releaseErr
}
func (f *fakeTransport) Close() error { f.calls = append(f.calls, "close"); return nil }
func testDevice() Device {
	return Device{Vendor: VendorID, Product: ProductID, BCDDevice: 792, LocationID: 0x100000, Configuration: 1, Interfaces: []Interface{
		{Number: 2, Class: 255, Endpoints: []Endpoint{{Address: 0x83, Attributes: 2, MaxPacketSize: 512}, {Address: 0x03, Attributes: 2, MaxPacketSize: 512}}},
		{Number: 4, Class: 2, Subclass: 6, Endpoints: []Endpoint{{Address: 0x85, Attributes: 3, MaxPacketSize: 16}}},
		{Number: 5, Class: 10, Endpoints: []Endpoint{{Address: 0x86, Attributes: 2, MaxPacketSize: 512}, {Address: 0x06, Attributes: 2, MaxPacketSize: 512}}},
	}}
}
func offlineSession(f *fakeTransport) *Session {
	d := testDevice()
	s := NewGate1Session(f)
	// Test-only synthetic approval. No equivalent production configuration exists.
	s.approved = []profile{{layout: d, iface: 2, in: 0x83, out: 0x03}}
	return s
}
func TestProductionStartsLockedWithZeroIO(t *testing.T) {
	f := &fakeTransport{reply: "OK"}
	s := NewGate1Session(f)
	if err := s.Connect(context.Background(), testDevice()); !errors.Is(err, ErrLocked) {
		t.Fatal(err)
	}
	if _, err := s.Query(context.Background(), "AT"); !errors.Is(err, ErrLocked) {
		t.Fatal(err)
	}
	if len(f.calls) != 0 {
		t.Fatal(f.calls)
	}
}
func TestUnknownLayoutNeverClaims(t *testing.T) {
	cases := map[string]func(*Device){
		"identity": func(d *Device) { d.Product++ }, "revision": func(d *Device) { d.BCDDevice++ }, "configuration": func(d *Device) { d.Configuration++ },
		"physical-location": func(d *Device) { d.LocationID = 0x200000 }, "endpoint": func(d *Device) { d.Interfaces[0].Endpoints[0].Address = 0x89 },
		"class": func(d *Device) { d.Interfaces[0].Class = 10 }, "alternate": func(d *Device) { d.Interfaces[0].Alternate = 1 },
		"ecm-layout": func(d *Device) { d.Interfaces[2].Number = 6 }, "extra-interface": func(d *Device) { d.Interfaces = append(d.Interfaces, Interface{Number: 7}) },
		"duplicate-endpoint": func(d *Device) {
			d.Interfaces[0].Endpoints = append(d.Interfaces[0].Endpoints, d.Interfaces[0].Endpoints[0])
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fakeTransport{}
			s := offlineSession(f)
			d := testDevice()
			mutate(&d)
			if err := s.Connect(context.Background(), d); err == nil {
				t.Fatal("accepted unknown layout")
			}
			if len(f.calls) != 0 {
				t.Fatal(f.calls)
			}
		})
	}
	for _, number := range []uint8{0, 1, 4, 5} {
		t.Run(string(rune('0'+number)), func(t *testing.T) {
			d := testDevice()
			d.Interfaces[0].Number = number
			f := &fakeTransport{}
			s := NewGate1Session(f)
			s.approved = []profile{{layout: d, iface: number, in: 0x83, out: 3}}
			if err := s.Connect(context.Background(), d); err == nil {
				t.Fatal("excluded interface accepted")
			}
			if len(f.calls) != 0 {
				t.Fatal(f.calls)
			}
		})
	}
}
func TestBusyStopsWithoutFallback(t *testing.T) {
	f := &fakeTransport{claimErr: errors.New("BUSY")}
	s := offlineSession(f)
	if s.Connect(context.Background(), testDevice()) == nil {
		t.Fatal("busy accepted")
	}
	if !reflect.DeepEqual(f.calls, []string{"claim", "close"}) {
		t.Fatal(f.calls)
	}
}
func TestNoProbeOrSMSAfterConnectAndAllWritesRejected(t *testing.T) {
	f := &fakeTransport{reply: "OK"}
	s := offlineSession(f)
	if err := s.Connect(context.Background(), testDevice()); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"AT+CMGD=1,4", "AT+CMGD=1", "AT+CMGS=10", "AT+CMGF=0", `AT+CPMS="ME","ME","ME"`, "AT+CMGL=4", "AT+CMGR=1", "AT+COPS=3,2", `AT+QCFG="usbnet",1`, `AT+QCFG="usbcfg",0x2c7c,0x0125`, "AT+CFUN=1,1", "AT+CCHO=foo", "AT+CGLA=1,4,0000", "AT+CPBW=1", "ATE0", "AT&F", "AT&W", "AT+QPCMV=1,2", "AT\rAT+CFUN=1,1", "AT;AT&W", "AT\x00", " AT", "AT ", "at", "AT+CSQ\n"} {
		if _, err := s.Query(context.Background(), cmd); err == nil {
			t.Errorf("accepted %q", cmd)
		}
	}
	if !reflect.DeepEqual(f.calls, []string{"claim"}) {
		t.Fatal(f.calls)
	}
	if _, err := s.Query(context.Background(), "AT+CSQ"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	_ = s.Close()
	if !reflect.DeepEqual(f.calls, []string{"claim", "AT+CSQ", "release", "close"}) {
		t.Fatal(f.calls)
	}
}
func TestModemErrorOrDisconnectClosesOnce(t *testing.T) {
	for _, reply := range []string{"ERROR", "+CMS ERROR: 500\r\nOK", "+CME ERROR: 10", "partial", "OK\nextra", "", "OKAY"} {
		t.Run(reply, func(t *testing.T) {
			f := &fakeTransport{reply: reply}
			s := offlineSession(f)
			_ = s.Connect(context.Background(), testDevice())
			if _, err := s.Query(context.Background(), "AT"); err == nil {
				t.Fatal("invalid reply accepted")
			}
			_ = s.Close()
			if !reflect.DeepEqual(f.calls, []string{"claim", "AT", "release", "close"}) {
				t.Fatal(f.calls)
			}
		})
	}
	f := &fakeTransport{queryErr: errors.New("NO_DEVICE"), releaseErr: errors.New("device gone")}
	s := offlineSession(f)
	_ = s.Connect(context.Background(), testDevice())
	if _, err := s.Query(context.Background(), "AT"); err == nil {
		t.Fatal("lost error")
	}
	_ = s.Close()
	if len(f.calls) != 4 {
		t.Fatal(f.calls)
	}
	if _, err := s.Query(context.Background(), "AT"); err == nil {
		t.Fatal("reopened automatically")
	}
}
func TestConcurrentQueriesAreSerialized(t *testing.T) {
	f := &fakeTransport{reply: "\r\n+CSQ: 20,99\r\nOK\r\n"}
	s := offlineSession(f)
	_ = s.Connect(context.Background(), testDevice())
	var wg sync.WaitGroup
	for n := 0; n < 30; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Query(context.Background(), "AT+CSQ"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	_ = s.Close()
	if f.maxInside != 1 {
		t.Fatal(f.maxInside)
	}
}
func TestCancelledContextDoesNotTouchTransport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &fakeTransport{}
	s := offlineSession(f)
	if s.Connect(ctx, testDevice()) == nil {
		t.Fatal("cancel ignored")
	}
	if len(f.calls) != 0 {
		t.Fatal(f.calls)
	}
}
func TestFixturesCannotApproveOrSmuggleJSON(t *testing.T) {
	for _, input := range []string{`{"schema":1,"gate1_approved":true,"devices":[]}`, `{"schema":1,"devices":[]} {}`, `{"schema":1,"allow_at":true,"devices":[]}`, `{"schema":2,"devices":[]}`} {
		if _, err := ReadFixture(strings.NewReader(input)); err == nil {
			t.Fatal(input)
		}
	}
}

func TestOversizedFixtureRejected(t *testing.T) {
	input := `{"schema":1,"devices":[]}` + strings.Repeat(" ", 1<<20)
	if _, err := ReadFixture(strings.NewReader(input)); err == nil {
		t.Fatal("oversized fixture accepted")
	}
}
