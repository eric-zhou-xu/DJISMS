package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/iniwex5/vohive/internal/safeusb"
	"github.com/iniwex5/vohive/internal/smsreceive"
	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"github.com/iniwex5/vohive/product/djisms-core/discovery"
	"github.com/iniwex5/vohive/product/djisms-core/host"
	"github.com/iniwex5/vohive/product/djisms-core/purge"
	"github.com/iniwex5/vohive/product/djisms-core/receive"
	"github.com/iniwex5/vohive/product/djisms-core/status"
	"github.com/warthog618/sms/encoding/tpdu"
	"github.com/warthog618/sms/encoding/ucs2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func device() discovery.Device {
	d := discovery.Device{RegistryID: 42, Location: 1234, Network: "fixture0", ECMOwners: 2, InterfaceMask: 63, Profile: safeusb.Device{Vendor: safeusb.VendorID, Product: safeusb.ProductID, BCDDevice: 792, LocationID: 1234}}
	raw, _ := hex.DecodeString(discovery.ProfileHex)
	_ = safeusb.ParseConfiguration(raw, &d.Profile)
	for n := 0; n < 6; n++ {
		f := discovery.Interface{RegistryID: uint64(n + 100), Number: n}
		switch n {
		case 2:
			f.Class = 255
			f.Endpoints = 3
		case 4:
			f.Class = 2
			f.Subclass = 6
			f.Endpoints = 1
			f.Owner = "AppleUserECM"
		case 5:
			f.Class = 10
			f.Alternate = 1
			f.Endpoints = 2
			f.Owner = "AppleUserECM"
		}
		d.Interfaces = append(d.Interfaces, f)
	}
	return d
}
func smsFixture(t *testing.T) smsreceive.RawMessage {
	t.Helper()
	p, e := tpdu.NewDeliver()
	if e != nil {
		t.Fatal(e)
	}
	p.OA = tpdu.NewAddress(tpdu.FromNumber("+15550000000"))
	p.SCTS = tpdu.Timestamp{Time: time.Date(2026, 9, 18, 3, 0, 0, 0, time.UTC)}
	p.DCS = 8
	p.UD = ucs2.Encode([]rune("离线链路测试"))
	b, e := p.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	return smsreceive.RawMessage{Index: 0, Status: 0, TPDULength: len(b), PDU: "00" + hex.EncodeToString(b)}
}

type statusMock struct{}

func (*statusMock) Connect(context.Context) error { return nil }
func (*statusMock) ExecutePlan(_ context.Context, save func(status.Report) error) (status.PlanReport, error) {
	r := status.Report{Command: "AT+CPIN?", ResponseLines: []string{"+CPIN: READY"}}
	return status.PlanReport{CloseSucceeded: true, Queries: []status.Report{r}}, save(r)
}

type receiveMock struct {
	message smsreceive.RawMessage
	cancel  context.CancelFunc
	final   bool
}

func (*receiveMock) Connect(context.Context) error { return nil }
func (m *receiveMock) Listen(ctx context.Context, _ receive.Config, s receive.Sink) (receive.Summary, error) {
	r := receive.Summary{Reconciled: true, Closed: true}
	if e := s.Snapshot(nil); e != nil {
		return r, e
	}
	if e := s.Ready(0); e != nil {
		return r, e
	}
	if m.final {
		m.cancel()
		return r, nil
	}
	if e := s.Save("usb_read", map[string]any{"valid_hex": "30303030", "diagnostic": map[string]any{"category": "ok"}}); e != nil {
		return r, e
	}
	e := s.Message(receive.Notice{Index: 0, ObservedUTC: time.Now().UTC().Format(time.RFC3339Nano)}, m.message)
	r.FinalUsed = 1
	return r, e
}

type purgeMock struct {
	message    smsreceive.RawMessage
	during     *bool
	called     *int
	fail       bool
	authorized func() error
}

func (*purgeMock) Connect(context.Context) error { return nil }
func (m *purgeMock) Purge(ctx context.Context, cfg purge.Config, s purge.Sink) (purge.Summary, error) {
	*m.called++
	*m.during = true
	e := s.Authorize(ctx, purge.Predelete{Inventory: []smsreceive.RawMessage{m.message}, Target: m.message, TargetObservedUTC: time.Now().UTC().Format(time.RFC3339Nano), Storage: "ME", Index: 0, PDUHash: cfg.TargetHash})
	*m.during = false
	if e != nil {
		return purge.Summary{Closed: true}, e
	}
	if m.authorized != nil {
		if e = m.authorized(); e != nil {
			return purge.Summary{}, e
		}
	}
	if m.fail {
		return purge.Summary{Closed: true, DeleteAttempted: true}, errors.New("injected unknown write result")
	}
	return purge.Summary{Closed: true, DeleteAttempted: true, DeleteAccepted: true, DeleteConfirmed: true, Reconciled: true}, nil
}
func testCore(t *testing.T) (*Core, discovery.Device) {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	os.Chmod(root, 0700)
	c, e := New(root, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Store.Close() })
	return c, device()
}
func setup(c *Core, d discovery.Device, m smsreceive.RawMessage, cancel context.CancelFunc, fail bool) (*int, *int) {
	calls, opens := new(int), new(int)
	during := false
	c.deps.snapshot = func() ([]discovery.Device, error) {
		x := d
		x.Interfaces = append([]discovery.Interface(nil), d.Interfaces...)
		if during {
			x.Interfaces[2].Owner = fmt.Sprintf("pid %d, fixture", os.Getpid())
		}
		return []discovery.Device{x}, nil
	}
	c.deps.capture = func(_ context.Context, x discovery.Device) (host.Snapshot, error) {
		return host.Snapshot{Device: x, IPv4: "192.0.2.1", InterfaceIndex: 7, DefaultGateway: "192.0.2.254", DefaultInterface: "fixture0", SIP: "enabled", HTTPS: true}, nil
	}
	c.deps.status = func(discovery.Device) statusTransport { return &statusMock{} }
	c.deps.receive = func(discovery.Device) receiveTransport {
		*opens++
		return &receiveMock{message: m, cancel: cancel, final: *opens > 1}
	}
	c.deps.purge = func(discovery.Device) purgeTransport {
		return &purgeMock{m, &during, calls, fail, func() error {
			summary, e := c.Store.Summary()
			if e != nil {
				return e
			}
			if summary["unfinished_delete_attempts"] != 1 {
				return errors.New("delete before durable intent")
			}
			return nil
		}}
	}
	return calls, opens
}
func TestReceiveArchiveNotificationPurgeAndRestartPolicy(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			c, d := testCore(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls, _ := setup(c, d, smsFixture(t), cancel, fail)
			e := c.connected(ctx, d)
			if fail && e == nil || !fail && e != nil {
				t.Fatal(e)
			}
			if *calls != 1 {
				t.Fatal("delete count", *calls)
			}
			summary, e2 := c.Store.Summary()
			if e2 != nil {
				t.Fatal(e2)
			}
			expected := 0
			if fail {
				expected = 1
			}
			if summary["unfinished_delete_attempts"] != expected {
				t.Fatal(summary)
			}
			pending, e2 := c.Store.PendingNotifications()
			if e2 != nil || len(pending) != 1 || pending[0].Body != "离线链路测试" {
				t.Fatal(pending, e2)
			}
			// Notification failure is an independent delivery outcome.
			if e2 = c.Store.NotificationResult(pending[0].ID, "failed", "injected notification failure"); e2 != nil {
				t.Fatal(e2)
			}
			if fail {
				c.stop(e)
				c.run(context.Background())
				state, _ := c.Current()
				if state.Phase != "safety_stop" || *calls != 1 {
					t.Fatal("unknown deletion retried")
				}
			}
		})
	}
}
func TestDiskFailurePreventsAnyPurge(t *testing.T) {
	c, d := testCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls, _ := setup(c, d, smsFixture(t), cancel, false)
	c.Store.Fault = func(point string) error {
		if point == "raw_durable" {
			return errors.New("injected disk fault")
		}
		return nil
	}
	if e := c.connected(ctx, d); e == nil {
		t.Fatal("failure ignored")
	}
	if *calls != 0 {
		t.Fatal("purged before archive")
	}
}

func TestUnavailableStateInvalidatesOldReadings(t *testing.T) {
	for _, phase := range []string{"safety_stop", "disconnected", "multiple_devices", "unsupported", "reconnecting", "checking"} {
		c, _ := testCore(t)
		c.state = State{Phase: "ready", Connected: true, Used: 8, Capacity: 23, SIM: "READY", LTE: "registered", Signal: "strong", Operator: "fixture", Network: "fixture0", LastVerified: "old"}
		c.update(func(s *State) { s.Phase = phase })
		got, _ := c.Current()
		if got.Connected || got.Capacity != 0 || got.Used != 0 || got.SIM != "" || got.LTE != "" || got.Network != "" || got.Signal != "" || got.Operator != "" {
			t.Fatalf("%s retains stale state: %+v", phase, got)
		}
		if got.LastVerified != "old" {
			t.Fatal("lost historical verification timestamp")
		}
	}
}

func TestEnablePurgeYieldsExistingSnapshotWithoutArrival(t *testing.T) {
	c, d := testCore(t)
	if e := c.SetPreferences(Preferences{true, false}); e != nil {
		t.Fatal(e)
	}
	s := &session{core: c, device: d, id: nonce(), inventory: map[int]record{}}
	if e := s.Snapshot([]smsreceive.RawMessage{smsFixture(t)}); e != nil {
		t.Fatal(e)
	}
	if e := s.Ready(1); e != nil {
		t.Fatal(e)
	}
	if s.shouldYield() {
		t.Fatal("disabled purge yielded")
	}
	if e := c.SetPreferences(Preferences{true, true}); e != nil {
		t.Fatal(e)
	}
	if !s.shouldYield() {
		t.Fatal("existing archived message did not request graceful yield")
	}
	if e := c.SetPreferences(Preferences{true, false}); e != nil {
		t.Fatal(e)
	}
	if s.shouldYield() {
		t.Fatal("withdrawn purge request still yielded")
	}
}

type receiveFunc func(context.Context, receive.Config, receive.Sink) (receive.Summary, error)

func (f receiveFunc) Connect(context.Context) error { return nil }
func (f receiveFunc) Listen(ctx context.Context, cfg receive.Config, s receive.Sink) (receive.Summary, error) {
	return f(ctx, cfg, s)
}

func TestAutomaticIdleReconnectPreservesInventory(t *testing.T) {
	for _, newRegistry := range []bool{false, true} {
		t.Run(fmt.Sprint(newRegistry), func(t *testing.T) {
			c, d := testCore(t)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			m := smsFixture(t)
			setup(c, d, m, cancel, false)
			c.deps.snapshot = func() ([]discovery.Device, error) { return []discovery.Device{d}, nil }
			if e := c.SetPreferences(Preferences{true, false}); e != nil {
				t.Fatal(e)
			}
			count := 0
			c.deps.receive = func(discovery.Device) receiveTransport {
				return receiveFunc(func(ctx context.Context, cfg receive.Config, s receive.Sink) (receive.Summary, error) {
					count++
					if count == 2 && (cfg.ExpectedUsed != 1 || cfg.KnownPDUHashes[0] != fmt.Sprintf("%x", sha256.Sum256([]byte(m.PDU)))) {
						t.Fatalf("missing recovery continuity: %+v", cfg)
					}
					if e := s.Snapshot([]smsreceive.RawMessage{m}); e != nil {
						return receive.Summary{}, e
					}
					if e := s.Ready(1); e != nil {
						return receive.Summary{}, e
					}
					if count == 1 {
						if newRegistry {
							d.RegistryID++
						}
						return receive.Summary{Closed: true, Interrupted: true}, errors.New("injected idle abort")
					}
					cancel()
					return receive.Summary{Closed: true, Reconciled: true}, nil
				})
			}
			c.run(ctx)
			state, _ := c.Current()
			if count != 2 || state.Phase == "safety_stop" {
				t.Fatalf("count %d state %+v", count, state)
			}
			if e := c.loadConnectionRecovery(); e != nil {
				t.Fatal(e)
			}
			if c.recoveryID != "" {
				t.Fatal("recovery checkpoint was not resolved")
			}
			if event, e := c.Store.LatestEvent("core_safety_stop"); e != nil || event != nil {
				t.Fatal(event, e)
			}
		})
	}
}

func TestConnectionCheckpointSurvivesRestart(t *testing.T) {
	c, _ := testCore(t)
	textHash, binaryHash := archive.PDUHashes(smsFixture(t).PDU)
	c.recoveryInventory = map[int]record{0: {strings.Repeat("a", 64), binaryHash, textHash}}
	if e := c.saveConnectionRecovery(42, "fixture"); e != nil {
		t.Fatal(e)
	}
	id := c.recoveryID
	c.recoveryInventory = nil
	c.recoveryID = ""
	if e := c.loadConnectionRecovery(); e != nil {
		t.Fatal(e)
	}
	if c.recoveryID != id || c.recoveryInventory[0].textHash != textHash {
		t.Fatal("checkpoint lost")
	}
}

func TestSleepClosesReceiveWithoutStartingPurge(t *testing.T) {
	c, d := testCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls, _ := setup(c, d, smsFixture(t), cancel, false)
	c.deps.receive = func(discovery.Device) receiveTransport {
		return receiveFunc(func(ctx context.Context, cfg receive.Config, s receive.Sink) (receive.Summary, error) {
			if e := s.Snapshot([]smsreceive.RawMessage{smsFixture(t)}); e != nil {
				return receive.Summary{}, e
			}
			if e := s.Ready(1); e != nil {
				return receive.Summary{}, e
			}
			if e := c.SetPower(true); e != nil {
				return receive.Summary{}, e
			}
			if !cfg.Yield() {
				t.Fatal("sleep did not request graceful boundary")
			}
			return receive.Summary{Closed: true, Reconciled: true}, nil
		})
	}
	e := c.connected(ctx, d)
	var interrupted idleInterruption
	if !errors.As(e, &interrupted) || *calls != 0 || len(c.recoveryInventory) != 1 {
		t.Fatalf("sleep result: %v purges %d", e, *calls)
	}
	if e = c.SetPower(false); e != nil {
		t.Fatal(e)
	}
	if c.isPaused() {
		t.Fatal("wake remained paused")
	}
}

func TestUnfinishedArchiveWriteBlocksDeviceAcquisition(t *testing.T) {
	c, _ := testCore(t)
	if e := os.WriteFile(filepath.Join(c.Store.Root, "raw", ".pending-incomplete"), []byte("{"), 0600); e != nil {
		t.Fatal(e)
	}
	c.deps.snapshot = func() ([]discovery.Device, error) {
		t.Fatal("device inspected despite unfinished write")
		return nil, nil
	}
	c.run(context.Background())
	state, _ := c.Current()
	if state.Phase != "safety_stop" {
		t.Fatal(state)
	}
}

func TestUnavailableEnvironmentNeverAcquiresInterface(t *testing.T) {
	for _, scenario := range []string{"multiple_devices", "interface_busy", "waiting_network"} {
		t.Run(scenario, func(t *testing.T) {
			c, d := testCore(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.emit = func(kind string, v any) {
				if kind == "state" && v.(State).Phase == scenario {
					cancel()
				}
			}
			c.deps.snapshot = func() ([]discovery.Device, error) {
				if scenario == "multiple_devices" {
					return []discovery.Device{d, d}, nil
				}
				if scenario == "interface_busy" {
					d.Interfaces[2].Owner = "another process"
				}
				return []discovery.Device{d}, nil
			}
			c.deps.capture = func(context.Context, discovery.Device) (host.Snapshot, error) {
				return host.Snapshot{}, host.NotReady{Err: errors.New("fixture network not ready")}
			}
			c.deps.status = func(discovery.Device) statusTransport { t.Fatal("acquired status interface"); return nil }
			c.deps.receive = func(discovery.Device) receiveTransport { t.Fatal("acquired receive interface"); return nil }
			c.run(ctx)
			state, _ := c.Current()
			if state.Phase != scenario || state.Connected {
				t.Fatal(state)
			}
			if stop, e := c.Store.LatestEvent("core_safety_stop"); e != nil || stop != nil {
				t.Fatal("transient environment latched", stop, e)
			}
		})
	}
}
