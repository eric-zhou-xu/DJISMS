package runtime

import (
	"errors"
	"fmt"
	"strings"

	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"github.com/iniwex5/vohive/product/djisms-core/atproto"
	"github.com/iniwex5/vohive/product/djisms-core/decoder"
	"github.com/iniwex5/vohive/product/djisms-core/status"
)

type statusHandoff struct {
	SourceID string             `json:"source_id"`
	Session  string             `json:"session"`
	Request  uint64             `json:"request_id"`
	Source   archive.SourceFact `json:"source"`
	Frame    atproto.Frame      `json:"frame"`
}

func objectOf(v any) (archive.M, error) {
	b, e := archive.Canonical(v)
	if e != nil {
		return nil, e
	}
	var m archive.M
	e = archive.Decode(b, &m)
	return m, e
}
func decodeObject(m archive.M, v any) error {
	b, e := archive.Canonical(m)
	if e != nil {
		return e
	}
	return archive.Decode(b, v)
}
func handoffID(session string, request uint64, offset int) string {
	return fmt.Sprintf("status-transport/%s/%d/%d", session, request, offset)
}
func (c *Core) completeStatusHandoff(h statusHandoff) error {
	if len(h.Session) != 32 || strings.Trim(h.Session, "0123456789abcdef") != "" || h.SourceID != handoffID(h.Session, h.Request, h.Frame.Offset) || !strings.HasPrefix(h.Source.Name, "session/"+h.Session+"/") {
		return errors.New("handoff identity mismatch")
	}
	facts, e := c.Store.SourceFacts(h.Source.Name)
	if e != nil {
		return e
	}
	matched := false
	for _, f := range facts {
		if f.Seq != h.Source.Seq || f.Name != h.Source.Name || f.Hash != h.Source.Hash || f.Observed != h.Source.Observed {
			continue
		}
		var r status.Report
		if e = archive.Decode(f.Data, &r); e != nil {
			return e
		}
		if r.RequestID != h.Request {
			return errors.New("request provenance mismatch")
		}
		frames, _ := status.ReplayFrames(r) // a later fault cannot invalidate an earlier fully bounded frame
		want, _ := archive.Canonical(h.Frame)
		for _, v := range frames {
			b, _ := archive.Canonical(v)
			if string(want) == string(b) {
				matched = true
			}
		}
	}
	if !matched {
		return errors.New("handoff frame not proven by immutable transport bytes")
	}
	if h.Frame.Kind != "CMT" {
		// CDS and CBM are preserved typed asynchronous evidence, never delivered as
		// an SMS-DELIVER or mistaken for a command response.
		return nil
	}
	meta := archive.M{"device_key": "dji:2ca3:4006:0318:ecm-v1", "connection_id": h.Session, "storage": nil, "index": nil, "received_at": h.Source.Observed, "acquisition_kind": "status_direct", "original_metadata": h.Frame, "transport_source_sha256": h.Source.Hash, "transport_source_name": h.Source.Name, "transport_source_seq": h.Source.Seq, "request_id": h.Request}
	// Status has not yet proven SIM identity. Do not backfill a later SIM into an
	// earlier observation or copy stale presentation state.
	rid, e := c.Store.Ingest(h.SourceID, h.Frame.PDU, meta, nil)
	if e != nil {
		return e
	}
	if e = decoder.Process(c.Store, []string{rid}, true); e != nil {
		return e
	}
	c.Publish("history_changed", nil)
	return nil
}
func (c *Core) stageStatus(session string, fact archive.SourceFact, r status.Report) error {
	frames, _ := status.ReplayFrames(r)
	if len(frames) == 0 {
		return nil
	}
	intents, e := c.Store.EventsOfKind("status_handoff_intent")
	if e != nil {
		return e
	}
	done, e := c.Store.EventsOfKind("status_handoff_complete")
	if e != nil {
		return e
	}
	completed := map[string]bool{}
	for _, v := range done {
		key, _ := v["source_id"].(string)
		completed[key] = true
	}
	for _, frame := range frames {
		id := handoffID(session, r.RequestID, frame.Offset)
		h := statusHandoff{SourceID: id, Session: session, Request: r.RequestID, Source: fact, Frame: frame}
		found := false
		for _, v := range intents {
			if v["source_id"] == id {
				if e = decodeObject(v, &h); e != nil {
					return e
				}
				a, _ := archive.Canonical(h.Frame)
				b, _ := archive.Canonical(frame)
				if string(a) != string(b) {
					return errors.New("handoff source reused with changed frame")
				}
				found = true
				break
			}
		}
		if completed[id] && !found {
			return errors.New("handoff completion lacks intent")
		}
		if completed[id] {
			continue
		}
		if !found {
			v, e := objectOf(h)
			if e != nil {
				return e
			}
			if e = c.Store.Event("status_handoff_intent", "", v); e != nil {
				return e
			}
		}
		if e = c.completeStatusHandoff(h); e != nil {
			return e
		}
		if e = c.Store.Event("status_handoff_complete", "", archive.M{"source_id": id, "kind": frame.Kind, "transport_source_sha256": h.Source.Hash}); e != nil {
			return e
		}
	}
	return nil
}
func (s *session) saveStatus(r status.Report) error {
	if e := s.Save("status_query", r); e != nil {
		return e
	}
	frames, _ := status.ReplayFrames(r)
	if len(frames) == 0 {
		return nil
	}
	name := fmt.Sprintf("session/%s/%06d/status_query", s.id, s.number)
	facts, e := s.core.Store.SourceFacts(name)
	if e != nil {
		return e
	}
	if len(facts) != 1 {
		return errors.New("ambiguous status source")
	}
	return s.core.stageStatus(s.id, facts[0], r)
}

// settleStatusHandoffs replays only explicit v2 status sessions, plus existing
// durable handoff intents. No device commands and no historical STOP clearing.
func (c *Core) settleStatusHandoffs() error {
	intents, e := c.Store.EventsOfKind("status_handoff_intent")
	if e != nil {
		return e
	}
	done, e := c.Store.EventsOfKind("status_handoff_complete")
	if e != nil {
		return e
	}
	completed := map[string]bool{}
	for _, v := range done {
		key, _ := v["source_id"].(string)
		completed[key] = true
	}
	for _, v := range intents {
		var h statusHandoff
		if e = decodeObject(v, &h); e != nil {
			return e
		}
		if completed[h.SourceID] {
			continue
		}
		if e = c.completeStatusHandoff(h); e != nil {
			return e
		}
		if e = c.Store.Event("status_handoff_complete", "", archive.M{"source_id": h.SourceID, "kind": h.Frame.Kind, "transport_source_sha256": h.Source.Hash}); e != nil {
			return e
		}
	}
	opens, e := c.Store.EventsOfKind("status_session_opened")
	if e != nil {
		return e
	}
	audits, e := c.Store.EventsOfKind("status_handoff_audited")
	if e != nil {
		return e
	}
	audited := map[string]bool{}
	for _, v := range audits {
		id, _ := v["session"].(string)
		audited[id] = true
	}
	for _, v := range opens {
		id, _ := v["session"].(string)
		if audited[id] {
			continue
		}
		facts, e := c.Store.SourceFacts("session/" + id + "/")
		if e != nil {
			return e
		}
		for _, f := range facts {
			if !strings.HasSuffix(f.Name, "/status_query") {
				continue
			}
			var r status.Report
			if e = archive.Decode(f.Data, &r); e != nil {
				return e
			}
			if e = c.stageStatus(id, f, r); e != nil {
				return e
			}
		}
	}
	return nil
}

func (c *Core) pendingStatusSession() (string, error) {
	opens, e := c.Store.EventsOfKind("status_session_opened")
	if e != nil {
		return "", e
	}
	closed, e := c.Store.EventsOfKind("status_session_closed")
	if e != nil {
		return "", e
	}
	done := map[string]bool{}
	for _, v := range closed {
		id, _ := v["session"].(string)
		done[id] = v["closed"] == true
	}
	for _, v := range opens {
		id, _ := v["session"].(string)
		if !done[id] {
			return id, nil
		}
	}
	return "", nil
}
