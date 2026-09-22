package simstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/iniwex5/vohive/internal/smsreceive"
	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"github.com/iniwex5/vohive/product/djisms-core/decoder"
	"github.com/iniwex5/vohive/product/djisms-core/discovery"
	"github.com/iniwex5/vohive/product/djisms-core/storageguard"
	"sort"
	"strings"
	"time"
)

type Request struct {
	Action    string `json:"action"`
	Indices   []int  `json:"indices,omitempty"`
	Plan      string `json:"plan,omitempty"`
	Confirmed bool   `json:"confirmed,omitempty"`
}
type Item struct {
	Index    int    `json:"index"`
	Status   int    `json:"status"`
	PDUHash  string `json:"pdu_sha256"`
	Receipt  string `json:"receipt"`
	Sender   string `json:"sender"`
	Time     string `json:"time"`
	Body     string `json:"body"`
	Archived bool   `json:"archived"`
	Reason   string `json:"reason,omitempty"`
}
type Result struct {
	ME        Storage    `json:"me"`
	SM        Storage    `json:"sm"`
	CPMS      [3]Storage `json:"cpms"`
	CNMI      string     `json:"cnmi"`
	SIM       string     `json:"sim"`
	SIMID     string     `json:"sim_id"`
	Supported string     `json:"supported"`
	Items     []Item     `json:"items"`
	Plan      string     `json:"plan,omitempty"`
	Expires   string     `json:"expires,omitempty"`
	Restored  bool       `json:"restored"`
	Error     string     `json:"error,omitempty"`
	Observed  string     `json:"observed"`
}
type plan struct {
	ID       string `json:"id"`
	SIM      string `json:"sim"`
	Registry uint64 `json:"registry"`
	Location uint32 `json:"location"`
	Expires  string `json:"expires"`
	Items    []Item `json:"items"`
}

func id() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func Pending(s *archive.Store) error { return storageguard.Check(s) }

type opener func(context.Context) (*Transport, discovery.Device, error)

func Run(ctx context.Context, s *archive.Store, req Request) (Result, error) {
	return run(ctx, s, req, nativeOpen)
}
func nativeOpen(ctx context.Context) (*Transport, discovery.Device, error) {
	ds, e := discovery.Snapshot()
	if e != nil {
		return nil, discovery.Device{}, e
	}
	if len(ds) != 1 {
		return nil, discovery.Device{}, errors.New("exactly one device required")
	}
	d := ds[0]
	if e = discovery.Validate(d); e != nil {
		return nil, d, e
	}
	t := NewNative(d.RegistryID, d.Location)
	if e = t.Connect(ctx); e != nil {
		return nil, d, e
	}
	return t, d, nil
}
func run(ctx context.Context, s *archive.Store, req Request, open opener) (out Result, err error) {
	out.Items = []Item{}
	out.Observed = time.Now().UTC().Format(time.RFC3339Nano)
	defer func() {
		if err != nil {
			out.Error = err.Error()
		}
	}()
	if e := baselineSafety(s); e != nil {
		return out, e
	}
	if e := Pending(s); e != nil {
		return out, e
	}
	summary, e := s.Summary()
	if e != nil {
		return out, e
	}
	if summary["unfinished_delete_attempts"] != 0 || summary["pending_temp_files"] != 0 {
		return out, errors.New("unfinished destructive/archive state")
	}
	if req.Action == "check" {
		out.Restored = true
		return out, nil
	}
	if req.Action != "inspect" && req.Action != "plan" && req.Action != "delete" {
		return out, errors.New("unsupported SIM action")
	}
	var approved plan
	if req.Action == "delete" {
		if !req.Confirmed || req.Plan == "" {
			return out, errors.New("explicit dry-run confirmation required")
		}
		events, e := s.EventsOfKind("sim_delete_plan")
		if e != nil {
			return out, e
		}
		found := false
		for _, v := range events {
			b, _ := archive.Canonical(v)
			var p plan
			if json.Unmarshal(b, &p) == nil && p.ID == req.Plan {
				approved = p
				found = true
			}
		}
		if !found {
			return out, errors.New("plan not found")
		}
		consumed, e := s.EventsOfKind("sim_delete_plan_consumed")
		if e != nil {
			return out, e
		}
		for _, v := range consumed {
			if v["id"] == req.Plan {
				return out, errors.New("plan already consumed; never replay")
			}
		}
		expires, e := time.Parse(time.RFC3339Nano, approved.Expires)
		if e != nil || time.Now().After(expires) {
			return out, errors.New("dry-run plan expired")
		}
	}
	t, d, e := open(ctx)
	if e != nil {
		return out, e
	}
	if req.Action == "delete" && (d.RegistryID != approved.Registry || d.Location != approved.Location) {
		return out, errors.Join(errors.New("attachment changed since dry run"), t.Close())
	}
	sessionID := id()
	ss := &session{t: t, store: s, id: sessionID}
	selected := false
	transportClosed := false
	if e = s.Event("sim_management_open_intent", "", archive.M{"session": sessionID, "device": d}); e != nil {
		return out, errors.Join(e, t.Close())
	}
	defer func() {
		if transportClosed && ss.frame.boundary() && ss.frame.active == nil {
			x := s.Event("sim_management_closed", "", archive.M{"session": sessionID, "classified_boundary": true})
			if x != nil {
				out.Restored = false
			}
			err = errors.Join(err, x)
		}
	}()
	defer func() {
		if !selected {
			closeErr := t.Close()
			transportClosed = closeErr == nil
			err = errors.Join(err, closeErr)
		}
	}()
	ss.onDirect = func(v Direct, source string) error { return preserveDirect(s, sessionID, v, source) }

	if e = ss.read(ctx); e != nil {
		return out, e
	}
	if !ss.frame.boundary() {
		return out, errors.New("unresolved prewrite input")
	}
	query := func(k uint8) (Response, error) { return ss.query(ctx, Command{Kind: k}) }
	r, e := query(1)
	if e != nil {
		return out, e
	}
	if len(r.Lines) != 1 || strings.ReplaceAll(r.Lines[0], " ", "") != "+CMGF:0" {
		return out, errors.New("PDU mode required; no CMGF setter")
	}
	r, e = query(2)
	if e != nil {
		return out, e
	}
	original, e := parseStorage(r)
	if e != nil {
		return out, e
	}
	for _, v := range original {
		if v.Name != "ME" {
			return out, errors.New("baseline CPMS must be ME/ME/ME; no guessed restore")
		}
	}
	out.ME = original[0]
	out.CPMS = original
	r, e = query(3)
	if e != nil {
		return out, e
	}
	out.CNMI = strings.TrimSpace(r.Lines[0])
	if strings.ReplaceAll(out.CNMI, " ", "") != "+CNMI:2,1,0,0,0" {
		return out, errors.New("CNMI differs from accepted receive baseline; no setter")
	}
	r, e = query(4)
	if e != nil {
		return out, e
	}
	out.SIM = strings.TrimSpace(strings.TrimPrefix(r.Lines[0], "+QCCID:"))
	if len(out.SIM) < 18 || len(out.SIM) > 22 || strings.Trim(out.SIM, "0123456789") != "" {
		return out, errors.New("SIM identity invalid")
	}
	if req.Action == "delete" && out.SIM != approved.SIM {
		return out, errors.New("SIM changed since dry run")
	}
	out.SIMID = archive.Hash([]byte(out.SIM))
	r, e = query(10)
	if e != nil {
		return out, e
	}
	out.Supported = r.Lines[0]
	if !supportsSM(out.Supported) {
		out.Restored = true
		return out, errors.New("device did not advertise SM read storage")
	}
	if e = s.Event("sim_selection_intent", "", archive.M{"session": sessionID, "device": d, "sim": out.SIM, "original": original, "cnmi": out.CNMI, "scope": "mem1 only; mem2/mem3 unchanged"}); e != nil {
		return out, e
	}
	selected = true
	// A failed/ambiguous selection remains durable and fail-closed. No speculative restoration across an unclassified response.
	defer func() {
		if !selected {
			return
		}
		if ss.frame.fault == nil && ss.frame.active == nil && ss.frame.boundary() {
			restoreCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_, x := ss.query(restoreCtx, Command{Kind: 8})
			if x == nil {
				var rr Response
				rr, x = ss.query(restoreCtx, Command{Kind: 2})
				if x == nil {
					var got [3]Storage
					got, x = parseStorage(rr)
					if x == nil {
						for i := range got {
							if got[i].Name != original[i].Name || got[i].Total != original[i].Total {
								x = errors.New("CPMS restoration mismatch")
							}
						}
					}
				}
			}
			if x == nil {
				rr, z := ss.query(restoreCtx, Command{Kind: 3})
				x = z
				if x == nil && (len(rr.Lines) != 1 || strings.TrimSpace(rr.Lines[0]) != out.CNMI) {
					x = errors.New("CNMI changed")
				}
			}
			if x == nil {
				x = s.Event("sim_selection_restored", "", archive.M{"session": sessionID, "original_names": [3]string{"ME", "ME", "ME"}, "cnmi": out.CNMI})
			}
			if x == nil {
				out.Restored = true
			}
			err = errors.Join(err, x)
		}
		closeErr := t.Close()
		transportClosed = closeErr == nil
		err = errors.Join(err, closeErr)
		if !out.Restored {
			err = errors.Join(err, errors.New("selection restoration unproven; receive/delete blocked"))
		}
	}()
	if _, e = query(6); e != nil {
		return out, e
	}
	readInventory := func() (map[int]smsreceive.RawMessage, error) {
		rr, e := query(2)
		if e != nil {
			return nil, e
		}
		st, e := parseStorage(rr)
		if e != nil {
			return nil, e
		}
		if st[0].Name != "SM" || st[1].Name != "ME" || st[2].Name != "ME" {
			return nil, errors.New("SM/ME/ME selection mismatch")
		}
		out.SM = st[0]
		out.ME = st[1]
		rr, e = query(5)
		if e != nil {
			return nil, e
		}
		m, e := inventory(rr, st[0].Used, st[0].Total)
		if e != nil {
			return nil, e
		}
		rr, e = query(2)
		if e != nil {
			return nil, e
		}
		after, e := parseStorage(rr)
		if e != nil || after != st {
			return nil, errors.New("inventory changed during read")
		}
		return m, nil
	}
	current, e := readInventory()
	if e != nil {
		return out, e
	}
	archiveItem := func(v smsreceive.RawMessage) (Item, error) {
		pdu, e := hexPDU(v)
		if e != nil {
			return Item{}, e
		}
		meta := archive.M{"device_key": "dji:2ca3:4006:0318:ecm-v1", "connection_id": sessionID, "received_at": out.Observed, "storage": "SM", "index": v.Index, "original_metadata": v, "sim_id": archive.Hash([]byte(out.SIM)), "acquisition_kind": "sim_inventory", "transport_source_prefix": "sim-storage/" + sessionID + "/"}
		rid, e := s.Ingest(fmt.Sprintf("sim-management/%s/snapshot/%d/%d", sessionID, ss.seq, v.Index), v.PDU, meta, nil)
		if e != nil {
			return Item{}, e
		}
		if e = decoder.Process(s, []string{rid}, false); e != nil {
			return Item{}, e
		}
		a, e := s.Receipt(rid)
		if e != nil {
			return Item{}, e
		}
		eligible, e := s.Eligible(rid)
		if e != nil {
			return Item{}, e
		}
		i := Item{Index: v.Index, Status: v.Status, PDUHash: archive.Hash(pdu), Receipt: rid, Archived: eligible}
		i.Sender, _ = a["sender"].(string)
		i.Body, _ = a["body"].(string)
		if raw, ok := a["decoded_json"].(string); ok {
			var z map[string]any
			json.Unmarshal([]byte(raw), &z)
			i.Time, _ = z["timestamp"].(string)
		}
		if !eligible {
			i.Reason = "未建立可删除的完整归档/解码证明"
		}
		return i, nil
	}
	keys := []int{}
	for k := range current {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		i, e := archiveItem(current[k])
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, i)
	}
	if req.Action == "plan" {
		p := plan{ID: id(), SIM: out.SIM, Registry: d.RegistryID, Location: d.Location, Expires: time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339Nano)}
		seen := map[int]bool{}
		for _, idx := range req.Indices {
			if seen[idx] {
				return out, errors.New("duplicate selection")
			}
			seen[idx] = true
			found := false
			for _, i := range out.Items {
				if i.Index == idx {
					if !i.Archived {
						return out, errors.New("selected SMS lacks reliable Mac archive")
					}
					p.Items = append(p.Items, i)
					found = true
				}
			}
			if !found {
				return out, errors.New("selected index no longer exists")
			}
		}
		if len(p.Items) == 0 {
			return out, errors.New("empty selection")
		}
		b, _ := json.Marshal(p)
		var v archive.M
		archive.Decode(b, &v)
		if e = s.Event("sim_delete_plan", "", v); e != nil {
			return out, e
		}
		out.Plan = p.ID
		out.Expires = p.Expires
		out.Items = p.Items
		return out, nil
	}
	if req.Action == "delete" {
		if len(approved.Items) == 0 {
			return out, errors.New("empty approved plan")
		}
		for _, i := range approved.Items {
			v, ok := current[i.Index]
			p, _ := hexPDU(v)
			if !ok || archive.Hash(p) != i.PDUHash {
				return out, errors.New("approved target changed")
			}
			a, e := s.Receipt(i.Receipt)
			if e != nil || a["pdu_bytes_sha256"] != i.PDUHash {
				return out, errors.New("approved archive differs")
			}
			if _, e = s.Verify(i.Receipt); e != nil {
				return out, e
			}
		}
		if e = s.Event("sim_delete_plan_consumed", "", archive.M{"id": approved.ID, "session": sessionID, "user_confirmed": true}); e != nil {
			return out, e
		}
		for _, target := range approved.Items {
			before, e := readInventory()
			if e != nil {
				return out, e
			}
			v, ok := before[target.Index]
			p, _ := hexPDU(v)
			if !ok || archive.Hash(p) != target.PDUHash {
				return out, errors.New("fresh target differs")
			}
			rr, e := ss.query(ctx, Command{Kind: 7, Index: target.Index})
			if e != nil {
				return out, e
			}
			if len(rr.Messages) != 1 || rr.Messages[0].PDU != v.PDU {
				return out, errors.New("CMGR target mismatch")
			}
			fresh, e := archiveItem(rr.Messages[0])
			if e != nil || !fresh.Archived {
				return out, errors.New("fresh target archive unproven")
			}
			before, e = readInventory()
			if e != nil {
				return out, e
			}
			v, ok = before[target.Index]
			p, _ = hexPDU(v)
			if !ok || archive.Hash(p) != target.PDUHash {
				return out, errors.New("target changed after read")
			}
			attempt := "sim-user-" + id()
			if e = s.Event("delete_intent", fresh.Receipt, archive.M{"attempt_id": attempt, "storage": "SM", "index": target.Index, "pdu_sha256": target.PDUHash, "connection_id": sessionID, "mode": "approved_live", "plan": approved.ID, "confirmed_scope": "explicit user-selected SM exact current PDU only"}); e != nil {
				return out, e
			}
			_, e = ss.query(ctx, Command{Kind: 9, Index: target.Index})
			var after map[int]smsreceive.RawMessage
			if e == nil {
				after, e = readInventory()
			}
			if e == nil {
				e = proveDeletion(before, after, target.Index)
			}
			if e != nil {
				x := s.Event("delete_result", fresh.Receipt, archive.M{"attempt_id": attempt, "state": "unknown", "response": e.Error()})
				return out, errors.Join(e, x)
			}
			if e = s.Event("delete_result", fresh.Receipt, archive.M{"attempt_id": attempt, "state": "confirmed", "response": "SM target absent; every other index/PDU/status unchanged; exact per-index operation"}); e != nil {
				return out, e
			}
			if e = s.Event("sim_delete_confirmation", fresh.Receipt, archive.M{"attempt_id": attempt, "plan": approved.ID, "before": fingerprint(before), "after": fingerprint(after), "storage": "SM", "index": target.Index}); e != nil {
				return out, e
			}
			current = after
		}
		out.Items = []Item{}
		for _, v := range current {
			i, e := archiveItem(v)
			if e != nil {
				return out, e
			}
			out.Items = append(out.Items, i)
		}
	}
	return out, nil
}

func preserveDirect(s *archive.Store, sessionID string, v Direct, source string) error {
	sourceID := fmt.Sprintf("sim-management/%s/direct/%d", sessionID, v.EventID)
	meta := archive.M{"device_key": "dji:2ca3:4006:0318:ecm-v1", "connection_id": sessionID, "received_at": v.ObservedUTC, "storage": nil, "index": nil, "original_metadata": v, "transport_source_sha256": source, "acquisition_kind": "sim_management_direct"}
	if e := s.Event("direct_handoff_intent", "", archive.M{"source_id": sourceID, "metadata": meta, "pdu": v.PDU}); e != nil {
		return e
	}
	rid, e := s.Ingest(sourceID, v.PDU, meta, nil)
	if e != nil {
		return e
	}
	if e = decoder.Process(s, []string{rid}, true); e != nil {
		return e
	}
	return s.Event("direct_handoff_complete", rid, archive.M{"source_id": sourceID})
}
