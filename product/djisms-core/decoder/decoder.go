// Package decoder transforms only verified durable PDU facts.
package decoder

import (
	"encoding/json"
	"fmt"
	"github.com/iniwex5/vohive/internal/smsarchive"
	"github.com/iniwex5/vohive/internal/smsreceive"
	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"strings"
)

func rawMessage(s *archive.Store, rid string, index int) (smsreceive.RawMessage, error) {
	var m smsreceive.RawMessage
	env, e := s.Verify(rid)
	if e != nil {
		return m, e
	}
	metadata, ok := env["metadata"].(map[string]any)
	if !ok {
		return m, fmt.Errorf("missing raw metadata")
	}
	raw, e := json.Marshal(metadata["original_metadata"])
	if e != nil {
		return m, e
	}
	if e = json.Unmarshal(raw, &m); e != nil {
		return m, e
	}
	pdu, _ := env["raw_pdu"].(string)
	if m.PDU != pdu {
		return m, fmt.Errorf("raw provenance mismatch")
	}
	// Decoder-local virtual indices allow fragments already purged from the modem.
	// Direct Class 0 has no stored status; zero here describes receive decoding only.
	m.Index = index
	if metadata["storage"] == nil {
		m.Status = 0
	}
	return m, nil
}
func decoded(v smsarchive.Record) archive.M {
	raw, _ := json.Marshal(v)
	m := archive.M{}
	_ = archive.Decode(raw, &m)
	state := "readable"
	switch {
	case v.BinaryHex != "":
		state = "binary"
	case v.Total > 0 && (v.Error == "" || strings.HasPrefix(v.Error, "fragment decode deferred:")):
		state = "fragment"
	case v.Error != "":
		state = "decode_error"
	}
	m["state"] = state
	m["sender"] = nil
	m["text"] = nil
	if v.Sender != "" {
		m["sender"] = v.Sender
	}
	if state == "readable" || state == "fragment" {
		m["text"] = v.Text
	}
	// Empty binary payload still needs a truthful binary state.
	if v.Error == "" && v.DCS&0x0c == 4 {
		m["state"] = "binary"
		m["text"] = nil
	}
	return m
}
func Process(s *archive.Store, receiptIDs []string, notify bool) error {
	for _, rid := range receiptIDs {
		m, e := rawMessage(s, rid, 0)
		if e != nil {
			return e
		}
		raw, e := archive.Canonical(smsarchive.Archive{SourceReceiptSHA256: rid, Messages: []smsreceive.RawMessage{m}})
		if e != nil {
			return e
		}
		result, e := smsarchive.Decode(raw)
		if e != nil {
			return e
		}
		v := result.Records[0]
		d := decoded(v)
		if e = s.SetDecoded(rid, d); e != nil {
			return e
		}
		if d["state"] == "fragment" {
			continue
		}
		state := "complete"
		body := v.Text
		if d["state"] == "binary" {
			state = "binary"
			body = ""
		}
		if d["state"] == "decode_error" {
			state = "decode_error"
			body = ""
		}
		if _, e = s.Materialize([]string{rid}, state, v.Sender, body, notify); e != nil {
			return e
		}
	}
	candidates, e := s.FragmentCandidates()
	if e != nil {
		return e
	}
	if len(candidates) == 0 {
		return s.ConsolidateHistory()
	}
	messages := []smsreceive.RawMessage{}
	for i, rid := range candidates {
		m, e := rawMessage(s, rid, i)
		if e != nil {
			return e
		}
		messages = append(messages, m)
	}
	raw, e := archive.Canonical(smsarchive.Archive{SourceReceiptSHA256: archive.Hash([]byte(strings.Join(candidates, ","))), Messages: messages})
	if e != nil {
		return e
	}
	result, e := smsarchive.Decode(raw)
	if e != nil {
		return e
	}
	newSet := map[string]bool{}
	for _, id := range receiptIDs {
		newSet[id] = true
	}
	for _, g := range result.Groups {
		parts := []string{}
		hasNew := false
		for _, idx := range g.Indices {
			parts = append(parts, candidates[idx])
			hasNew = hasNew || newSet[candidates[idx]]
		}
		if !hasNew {
			continue
		}
		state := "incomplete"
		body := ""
		if g.State == "complete" {
			state = "complete"
			body = g.Text
		}
		if _, e = s.Materialize(parts, state, result.Records[g.Indices[0]].Sender, body, notify && hasNew); e != nil {
			return e
		}
	}
	return s.ConsolidateHistory()
}
