package runtime

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"github.com/iniwex5/vohive/product/djisms-core/decoder"
	"github.com/iniwex5/vohive/product/djisms-core/purge"
	"github.com/iniwex5/vohive/product/djisms-core/receive"
)

func (s *session) openTransport(kind string) error {
	state, _ := s.core.Current()
	return s.core.Store.Event("transport_session_opened", "", archive.M{"session": s.id, "transport": kind, "sim_id": state.SIMID, "sim_number": state.SIMNumber})
}
func (c *Core) directTransportHandoff(session string, d receive.Direct, origin archive.M) error {
	if len(session) != 32 || strings.Trim(session, "0123456789abcdef") != "" || d.EventID < 1 || d.ObservedUTC == "" {
		return errors.New("invalid direct provenance")
	}
	id := fmt.Sprintf("product/%s/direct/%d", session, d.EventID)
	intents, e := c.Store.EventsOfKind("direct_handoff_intent")
	if e != nil {
		return e
	}
	var intent archive.M
	meta := archive.M{"device_key": "dji:2ca3:4006:0318:ecm-v1", "connection_id": session, "storage": nil, "index": nil, "received_at": d.ObservedUTC, "acquisition_kind": "direct", "original_metadata": d, "sim_id": origin["sim_id"], "sim_number": origin["sim_number"]}
	for _, v := range intents {
		if v["source_id"] == id {
			intent = v
			old, _ := archive.Canonical(v["metadata"])
			fresh, _ := archive.Canonical(meta)
			if string(old) != string(fresh) || v["pdu"] != d.PDU {
				return errors.New("direct identity changed")
			}
			break
		}
	}
	if intent == nil {
		intent = archive.M{"source_id": id, "metadata": meta, "pdu": d.PDU}
		if e = c.Store.Event("direct_handoff_intent", "", intent); e != nil {
			return e
		}
	}
	return c.finishDirectIntent(intent)
}
func (c *Core) finishDirectIntent(intent archive.M) error {
	id, ok := intent["source_id"].(string)
	if !ok {
		return errors.New("invalid handoff source")
	}
	pdu, ok := intent["pdu"].(string)
	if !ok {
		return errors.New("invalid handoff PDU")
	}
	meta, ok := intent["metadata"].(map[string]any)
	if !ok {
		return errors.New("invalid handoff metadata")
	}
	done, e := c.Store.EventsOfKind("direct_handoff_complete")
	if e != nil {
		return e
	}
	for _, v := range done {
		if v["source_id"] == id {
			return nil
		}
	}
	rid, e := c.Store.Ingest(id, pdu, meta, nil)
	if e != nil {
		return e
	}
	if e = decoder.Process(c.Store, []string{rid}, true); e != nil {
		return e
	}
	if e = c.Store.Event("direct_handoff_complete", rid, archive.M{"source_id": id}); e != nil {
		return e
	}
	c.Publish("history_changed", nil)
	return nil
}

// Replays only new explicitly marked transport sessions. Existing receipt IDs,
// timestamps, SIM stamps and source namespaces are retained without mutation.
func (c *Core) settleDirectTransports() error {
	intents, e := c.Store.EventsOfKind("direct_handoff_intent")
	if e != nil {
		return e
	}
	for _, v := range intents {
		if e = c.finishDirectIntent(v); e != nil {
			return e
		}
	}
	opens, e := c.Store.EventsOfKind("transport_session_opened")
	if e != nil {
		return e
	}
	closed, e := c.Store.EventsOfKind("transport_handoff_audited")
	if e != nil {
		return e
	}
	done := map[string]bool{}
	for _, v := range closed {
		id, _ := v["session"].(string)
		done[id] = true
	}
	for _, origin := range opens {
		id, _ := origin["session"].(string)
		if done[id] {
			continue
		}
		facts, e := c.Store.SourceFacts("session/" + id + "/")
		if e != nil {
			return e
		}
		events := []struct {
			Kind string
			Data []byte
		}{}
		for _, f := range facts {
			events = append(events, struct {
				Kind string
				Data []byte
			}{filepath.Base(f.Name), f.Data})
		}
		var ds []receive.Direct
		var replayErr error
		if origin["transport"] == "receive" {
			ds, replayErr = receive.ReplayDirectFacts(events)
		} else if origin["transport"] == "purge" {
			pds, e := purge.ReplayDirectFacts(events)
			replayErr = e
			for _, d := range pds {
				ds = append(ds, receive.Direct{EventID: d.EventID, ObservedUTC: d.ObservedUTC, Offset: d.Offset, Header: d.Header, HeaderHex: d.HeaderHex, TPDULength: d.TPDULength, PDU: d.PDU, PDULineHex: d.PDULineHex})
			}
		} else {
			return errors.New("unknown transport handoff")
		}
		for _, d := range ds {
			if e = c.directTransportHandoff(id, d, origin); e != nil {
				return e
			}
		}
		if replayErr != nil {
			return replayErr
		}
		if e = c.Store.Event("transport_handoff_audited", "", archive.M{"session": id, "replayed_after_interruption": true}); e != nil {
			return e
		}
	}
	return nil
}
