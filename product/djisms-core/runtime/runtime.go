// Package runtime serializes ownership of IF2 and all receive/purge lifecycles.
package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/iniwex5/vohive/internal/smsreceive"
	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"github.com/iniwex5/vohive/product/djisms-core/buildinfo"
	"github.com/iniwex5/vohive/product/djisms-core/decoder"
	"github.com/iniwex5/vohive/product/djisms-core/discovery"
	"github.com/iniwex5/vohive/product/djisms-core/host"
	"github.com/iniwex5/vohive/product/djisms-core/receive"
	"github.com/iniwex5/vohive/product/djisms-core/status"
	"sort"
	"sync"
	"time"
)

type State struct {
	IPv4           string `json:"ipv4,omitempty"`
	LastRecovery   string `json:"last_recovery,omitempty"`
	SIMID          string `json:"sim_id,omitempty"`
	SIMNumber      string `json:"sim_number,omitempty"`
	Phase          string `json:"phase"`
	Detail         string `json:"detail"`
	Connected      bool   `json:"connected"`
	Used           int    `json:"used"`
	Capacity       int    `json:"capacity"`
	SIM            string `json:"sim"`
	LTE            string `json:"lte"`
	Signal         string `json:"signal"`
	Operator       string `json:"operator"`
	Network        string `json:"network_interface"`
	LastVerified   string `json:"last_verified"`
	StatusObserved string `json:"status_observed"`
	Session        string `json:"session"`
}
type Preferences struct {
	Notifications bool `json:"notifications"`
	AutoPurge     bool `json:"auto_purge"`
}
type Core struct {
	deps                  dependencies
	Store                 *archive.Store
	mu                    sync.Mutex
	state                 State
	prefs                 Preferences
	emit                  func(string, any)
	cancel                context.CancelFunc
	done                  chan struct{}
	purgeRevision         uint64
	recoveryInventory     map[int]record // owned exclusively by run
	recoveryID            string
	powerPaused           bool
	recoveryCheckpoint    *recoveryCheckpoint
	recoveryInactiveSince time.Time
}

func New(root string, emit func(string, any)) (*Core, error) {
	s, e := archive.Open(root)
	if e != nil {
		return nil, e
	}
	c := &Core{deps: nativeDependencies(), Store: s, prefs: Preferences{true, true}, emit: emit, done: make(chan struct{}), state: State{Phase: "starting", Detail: "正在核验本地永久档案", Capacity: 23}}
	p, e := s.LatestEvent("preferences")
	if e != nil {
		s.Close()
		return nil, e
	}
	if p != nil {
		c.prefs.Notifications, _ = p["notifications"].(bool)
		c.prefs.AutoPurge, _ = p["auto_purge"].(bool)
	}
	return c, nil
}
func (c *Core) Current() (State, Preferences) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state, c.prefs
}
func (c *Core) update(f func(*State)) {
	c.mu.Lock()
	previousPhase := c.state.Phase
	f(&c.state)
	// Unavailable phases must never retain the previous connection's readings.
	if c.state.Phase != "ready" && c.state.Phase != "archiving" && (c.state.Phase != "checking" || previousPhase != "checking") {
		c.state.Connected = false
		c.state.Used, c.state.Capacity = 0, 0
		c.state.SIM, c.state.LTE, c.state.Signal, c.state.Operator = "", "", "", ""
		c.state.Network = ""
	}
	s := c.state
	c.mu.Unlock()
	if c.emit != nil {
		c.emit("state", s)
	}
}
func (c *Core) Publish(kind string, v any) {
	if c.emit != nil {
		c.emit(kind, v)
	}
}
func (c *Core) SetPreferences(p Preferences) error {
	if e := c.Store.Event("preferences", "", archive.M{"notifications": p.Notifications, "auto_purge": p.AutoPurge}); e != nil {
		return e
	}
	c.mu.Lock()
	if p.AutoPurge && !c.prefs.AutoPurge {
		c.purgeRevision++
	}
	c.prefs = p
	c.mu.Unlock()
	c.Publish("preferences", p)
	return nil
}

// SetPower is a lifecycle hint, never permission to skip purge reconciliation.
func (c *Core) SetPower(paused bool) error {
	c.mu.Lock()
	c.powerPaused = paused
	c.mu.Unlock()
	return c.Store.Event("power_state", "", archive.M{"preparing_sleep": paused})
}
func (c *Core) isPaused() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.powerPaused
}
func (c *Core) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	c.cancel = cancel
	go func() { defer close(c.done); c.run(ctx) }()
}
func (c *Core) Close() error {
	if c.cancel != nil {
		c.cancel()
		<-c.done
	}
	return c.Store.Close()
}
func nonce() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func (c *Core) stop(e error) {
	_ = c.Store.Event("core_safety_stop", "", archive.M{"error": e.Error(), "when": time.Now().UTC().Format(time.RFC3339Nano)})
	c.update(func(s *State) {
		s.Phase = "safety_stop"
		s.Detail = "安全停止：" + e.Error() + "。已保存的原始短信保留；请先核查，程序不会重试删除。"
	})
}
func (c *Core) run(ctx context.Context) {
	stopped, e := c.Store.LatestEvent("core_safety_stop")
	if e != nil {
		c.stop(e)
		return
	}
	reviewed := false
	if stopped != nil {
		reviewed, e = c.reviewedStop(stopped)
		if e != nil {
			c.stop(e)
			return
		}
	}
	if stopped != nil && !reviewed {
		c.update(func(s *State) {
			s.Phase = "safety_stop"
			s.Detail = "上次安全停止尚待核查；自动接收及清理暂停。"
		})
		return
	}
	summary, e := c.Store.Summary()
	if e != nil {
		c.stop(e)
		return
	}
	if summary["unfinished_delete_attempts"] != 0 {
		c.stop(errors.New("unresolved delete intent/result; never retry"))
		return
	}
	if summary["pending_temp_files"] != 0 {
		c.stop(errors.New("unfinished archive write retained; no device operation until reviewed"))
		return
	}
	if e = c.loadConnectionRecovery(); e != nil {
		c.stop(e)
		return
	}
	ids, e := c.Store.Undecoded()
	if e == nil && len(ids) > 0 {
		e = decoder.Process(c.Store, ids, false)
	}
	if e != nil {
		c.stop(e)
		return
	}
	if e = c.Store.ConsolidateHistory(); e != nil {
		c.stop(e)
		return
	}
	if e = c.Store.Event("core_started", "", archive.M{"version": buildinfo.Version, "build_id": buildinfo.BuildID, "source_tree": buildinfo.SourceTree}); e != nil {
		c.stop(e)
		return
	}
	c.Publish("history_changed", nil)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if c.isPaused() {
			c.update(func(s *State) {
				s.Phase = "sleeping"
				s.Detail = "接收已暂停，等待系统唤醒后重新核验设备"
			})
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			continue
		}
		if e := c.sleepRecoveryDeadline(); e != nil {
			c.stop(e)
			return
		}
		devices, e := c.deps.snapshot()
		if e != nil {
			// The API intentionally tears down its device tree. No USB session
			// is held here; tolerate discovery churn only within its durable deadline.
			intent, intentError := c.sleepRecoveryIntent()
			if intentError == nil && intent != nil {
				if logError := c.Store.Event("sleep_usb_discovery_wait", "", archive.M{"checkpoint": c.recoveryID, "error": e.Error()}); logError != nil {
					c.stop(logError)
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
				continue
			}
			if intentError != nil {
				e = intentError
			}
			c.stop(e)
			return
		}
		if len(devices) == 0 {
			c.update(func(s *State) {
				s.Phase = "disconnected"
				s.Connected = false
				s.Detail = "请插入支持的 DJI 4G 模块"
			})
		} else if len(devices) > 1 {
			c.update(func(s *State) {
				s.Phase = "multiple_devices"
				s.Connected = false
				s.Detail = "检测到多个模块，请只连接一个；当前不会打开任何 AT 接口"
			})
		} else {
			d, e := discovery.Single(devices)
			if e != nil {
				c.update(func(s *State) {
					s.Phase = "unsupported"
					s.Connected = false
					s.Detail = "设备硬件或 ECM 配置不兼容：" + e.Error()
				})
			} else if d.Interfaces[2].Owner != "" {
				c.update(func(s *State) {
					s.Phase = "interface_busy"
					s.Detail = "模块短信接口正被其他程序使用；关闭占用它的程序后会自动重新检查"
				})
			} else {
				e = c.connected(ctx, d)
				if e != nil {
					var pending connectionPending
					if errors.As(e, &pending) {
						if e = c.trySleepRecovery(ctx, d, time.Now()); e != nil {
							c.stop(e)
							return
						}
						c.update(func(s *State) {
							s.Phase = "waiting_network"
							s.Detail = "等待模块的 4G 数据链路就绪；接收尚未启动，将自动再次核验"
						})
						select {
						case <-ctx.Done():
							return
						case <-ticker.C:
						}
						continue
					}
					// Only the transport's durable idle-boundary proof permits recovery.
					totals, sumErr := c.Store.Summary()
					var critical purgeFailure
					var interrupted idleInterruption
					if sumErr != nil || totals["unfinished_delete_attempts"] != 0 || errors.As(e, &critical) || !errors.As(e, &interrupted) {
						c.stop(e)
						return
					}
					if e = c.saveConnectionRecovery(d.RegistryID, e.Error(), d.Location); e != nil {
						c.stop(e)
						return
					}
					c.update(func(s *State) {
						s.Phase = "reconnecting"
						s.Connected = false
						s.Detail = "接收连接中断；正在等待设备并重新核验，原始短信已保留"
					})
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (c *Core) connected(ctx context.Context, d discovery.Device) error {
	if d.Interfaces[2].Owner != "" {
		return errors.New("Interface 2 is already owned; no takeover")
	}
	c.update(func(s *State) {
		s.Phase = "checking"
		s.Connected = true
		s.Network = d.Network
		s.Session = nonce()
		s.Detail = "正在核验设备、SIP 和现有 4G 数据链路"
	})
	before, e := c.deps.capture(ctx, d)
	if e != nil {
		var waiting host.NotReady
		if errors.As(e, &waiting) {
			return connectionPending{e}
		}
		return e
	}
	current := &session{core: c, device: d, id: nonce(), inventory: map[int]record{}}
	if e = current.Save("host_before", before); e != nil {
		return e
	}
	tr := c.deps.status(d)
	if e = tr.Connect(ctx); e != nil {
		return e
	}
	report, e := tr.ExecutePlan(ctx, func(r status.Report) error { return current.Save("status_query", r) })
	if e != nil {
		return e
	}
	if e = current.Save("status_complete", report); e != nil {
		return e
	}
	c.update(func(s *State) {
		s.StatusObserved = time.Now().UTC().Format(time.RFC3339Nano)
		for _, q := range report.Queries {
			if len(q.ResponseLines) == 1 {
				switch q.Command {
				case "AT+QCCID":
					s.SIMID = status.CardFingerprint(q.ResponseLines[0])
				case "AT+CNUM":
					s.SIMNumber = status.PhoneNumber(q.ResponseLines[0])
				case "AT+CPIN?":
					s.SIM = q.ResponseLines[0]
				case "AT+CEREG?":
					s.LTE = q.ResponseLines[0]
				case "AT+CSQ":
					s.Signal = q.ResponseLines[0]
				case "AT+COPS?":
					s.Operator = q.ResponseLines[0]
				}
			}
		}
	})
	if e = current.reconcile(ctx, before); e != nil {
		return e
	}
	for ctx.Err() == nil {
		if c.isPaused() {
			return nil
		}
		current = &session{core: c, device: d, id: nonce(), inventory: map[int]record{}}
		listenCtx, cancel := context.WithCancel(ctx)
		transport := c.deps.receive(d)
		if e = transport.Connect(listenCtx); e != nil {
			cancel()
			return e
		}
		known, hashes := map[int]bool{}, map[int]string{}
		for idx, saved := range c.recoveryInventory {
			known[idx] = true
			hashes[idx] = saved.textHash
		}
		result, e := transport.Listen(listenCtx, receive.Config{KnownIndices: known, KnownPDUHashes: hashes, ExpectedUsed: len(known), Yield: current.shouldYield}, current)
		cancel()
		if e != nil {
			if result.Interrupted && result.Closed {
				c.recoveryInventory = current.inventory
				return idleInterruption{e}
			}
			return e
		}
		c.recoveryInventory = nil
		if !result.Reconciled || !result.Closed {
			return errors.New("receive session did not reconcile")
		}
		if c.isPaused() {
			if e = current.Save("sleep_receive_closed", archive.M{"settings_reconciled": true, "closed": true, "host_check_deferred_until_wake": true}); e != nil {
				return e
			}
			c.recoveryInventory = current.inventory
			return idleInterruption{errors.New(sleepIdleReason)}
		}
		if e = current.reconcile(context.WithoutCancel(ctx), before); e != nil {
			return e
		}
		if ctx.Err() != nil {
			return nil
		}
		_, prefs := c.Current()
		if !prefs.AutoPurge {
			continue
		}
		slots := []int{}
		for idx := range current.inventory {
			slots = append(slots, idx)
		}
		sort.Ints(slots)
		for _, idx := range slots {
			if ctx.Err() != nil || c.isPaused() {
				return nil
			}
			_, prefs = c.Current()
			if !prefs.AutoPurge {
				break
			}
			target := current.inventory[idx]
			eligible, e := c.Store.Eligible(target.receipt)
			if e != nil {
				return e
			}
			if !eligible {
				continue
			}
			if e = c.purgeOne(ctx, d, current.inventory, idx); e != nil {
				return e
			}
			delete(current.inventory, idx)
		}
	}
	return nil
}

type record struct{ receipt, hash, textHash string }

type idleInterruption struct{ error }
type connectionPending struct{ error }
type session struct {
	core            *Core
	device          discovery.Device
	id              string
	number          int
	idleTimeouts    int
	lastIdleSummary time.Time
	inventory       map[int]record
	rawCount        int
	yieldWanted     bool
	lastArrival     time.Time
	purgeRevision   uint64
}

func (s *session) Save(kind string, v any) error {
	if kind == "session_end" && s.idleTimeouts > 0 {
		count := s.idleTimeouts
		s.idleTimeouts = 0
		if e := s.Save("idle_timeout_summary", archive.M{"samples": count, "count_is_not_actual_bytes": true}); e != nil {
			return e
		}
	}
	raw, e := archive.Canonical(v)
	if e != nil {
		return e
	}
	if kind == "usb_read" {
		var m struct {
			Hex        string `json:"valid_hex"`
			Diagnostic struct {
				Category string `json:"category"`
			} `json:"diagnostic"`
		}
		if e = archive.Decode(raw, &m); e != nil {
			return e
		}
		// Timeout byte counts are untrusted and are never treated as empty USB data.
		// No valid bytes exist in this diagnostic; aggregate idle samples at Pulse.
		if m.Hex == "" && m.Diagnostic.Category == "timeout" {
			s.idleTimeouts++
			return nil
		}
	}
	s.number++
	_, e = s.core.Store.SaveSource(fmt.Sprintf("session/%s/%06d/%s", s.id, s.number, kind), raw)
	return e
}
func (s *session) ingest(m smsreceive.RawMessage, kind, observed string, notify bool) (string, error) {
	meta := archive.M{"device_key": "dji:2ca3:4006:0318:ecm-v1", "connection_id": s.id, "storage": "ME", "index": m.Index, "received_at": observed, "acquisition_kind": kind, "original_metadata": m}
	state, _ := s.core.Current()
	meta["sim_id"], meta["sim_number"] = state.SIMID, state.SIMNumber
	rid, e := s.core.Store.Ingest(fmt.Sprintf("product/%s/%s/%d/%d", s.id, kind, m.Index, m.Offset), m.PDU, meta, nil)
	if e != nil {
		return "", e
	}
	if e = decoder.Process(s.core.Store, []string{rid}, notify); e != nil {
		return "", e
	}
	textHash, h := archive.PDUHashes(m.PDU)
	s.inventory[m.Index] = record{rid, h, textHash}
	s.core.Publish("history_changed", nil)
	return rid, nil
}
func (s *session) Snapshot(ms []smsreceive.RawMessage) error {
	if e := s.Save("snapshot_raw", ms); e != nil {
		return e
	}
	for _, m := range ms {
		if _, e := s.ingest(m, "snapshot", time.Now().UTC().Format(time.RFC3339Nano), false); e != nil {
			return e
		}
	}
	return nil
}
func (s *session) Message(n receive.Notice, m smsreceive.RawMessage) error {
	if e := s.Save("arrival_raw", archive.M{"notice": n, "message": m}); e != nil {
		return e
	}
	if _, e := s.ingest(m, "arrival", n.ObservedUTC, true); e != nil {
		return e
	}
	s.rawCount++
	s.core.update(func(v *State) { v.Used = len(s.inventory); v.Detail = "新短信已永久归档；正在完成核验" })
	s.yieldWanted = true
	s.lastArrival = time.Now()
	return nil
}
func (s *session) Direct(d receive.Direct) error {
	s.lastArrival = time.Now()
	if e := s.Save("direct_raw", d); e != nil {
		return e
	}
	meta := archive.M{"device_key": "dji:2ca3:4006:0318:ecm-v1", "connection_id": s.id, "storage": nil, "index": nil, "received_at": d.ObservedUTC, "acquisition_kind": "direct", "original_metadata": d}
	state, _ := s.core.Current()
	meta["sim_id"], meta["sim_number"] = state.SIMID, state.SIMNumber
	rid, e := s.core.Store.Ingest(fmt.Sprintf("product/%s/direct/%d", s.id, d.EventID), d.PDU, meta, nil)
	if e != nil {
		return e
	}
	if e = decoder.Process(s.core.Store, []string{rid}, true); e != nil {
		return e
	}
	s.core.Publish("history_changed", nil)
	return nil
}
func (s *session) Ready(used int) error {
	if s.core.recoveryID != "" {
		if e := s.core.Store.Event("receive_connection_restored", "", archive.M{"id": s.core.recoveryID, "session": s.id, "registry_id": s.device.RegistryID, "inventory_verified": true}); e != nil {
			return e
		}
		s.core.update(func(v *State) { v.LastRecovery = time.Now().UTC().Format(time.RFC3339Nano) })
		s.core.recoveryID = ""
		s.core.recoveryCheckpoint = nil
		s.core.recoveryInactiveSince = time.Time{}
	}
	s.core.update(func(v *State) {
		v.Phase = "ready"
		v.Connected = true
		v.Capacity = 23
		v.Network = s.device.Network
		v.Used = used
		v.Detail = "正在后台接收；原始短信永久保存在本机"
		v.Session = s.id
	})
	s.core.mu.Lock()
	s.purgeRevision = s.core.purgeRevision
	s.core.mu.Unlock()
	_, prefs := s.core.Current()
	if prefs.AutoPurge {
		for _, r := range s.inventory {
			ok, e := s.core.Store.Eligible(r.receipt)
			if e != nil {
				return e
			}
			if ok {
				s.yieldWanted = true
				s.lastArrival = time.Now()
				break
			}
		}
	}
	return nil
}
func (s *session) Pulse(n int) error {
	if s.idleTimeouts > 0 && time.Since(s.lastIdleSummary) >= time.Minute {
		if e := s.Save("idle_timeout_summary", archive.M{"samples": s.idleTimeouts, "count_is_not_actual_bytes": true}); e != nil {
			return e
		}
		s.idleTimeouts = 0
		s.lastIdleSummary = time.Now()
	}
	return nil
}
func (s *session) reconcile(ctx context.Context, before host.Snapshot) error {
	ds, e := s.core.deps.snapshot()
	if e != nil {
		return e
	}
	d, e := discovery.Single(ds)
	if e != nil {
		return e
	}
	after, e := s.core.deps.capture(ctx, d)
	if e != nil {
		return e
	}
	if e = s.Save("host_after", after); e != nil {
		return e
	}
	if e = host.Compare(before, after); e != nil {
		return e
	}
	s.core.update(func(v *State) { v.LastVerified = after.ObservedUTC; v.IPv4 = after.IPv4 })
	return nil
}

func (s *session) shouldYield() bool {
	s.core.mu.Lock()
	requested := s.core.powerPaused || (s.core.prefs.AutoPurge && s.purgeRevision != s.core.purgeRevision)
	s.core.mu.Unlock()
	// A preference transition only requests a graceful boundary. Eligibility and
	// fresh-read verification still run in the serialized purge path.
	if requested {
		return true
	}
	return s.yieldWanted && time.Since(s.lastArrival) >= 2*time.Second
}
