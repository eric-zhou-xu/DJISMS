package archive

import (
	"encoding/json"
	"errors"
	"github.com/iniwex5/vohive/product/djisms-core/database"
	"sort"
	"strings"
	"time"
)

type Message struct {
	SIMID             string `json:"sim_id,omitempty"`
	SIMNumber         string `json:"sim_number,omitempty"`
	ID                string `json:"id"`
	State             string `json:"state"`
	Sender            string `json:"sender"`
	Body              string `json:"body"`
	CreatedAt         string `json:"created_at"`
	NotificationState string `json:"notification_state"`
	Parts             int    `json:"parts"`
	SentAt            string `json:"sent_at,omitempty"`
}

func messageRow(r database.Row) Message {
	m := Message{ID: text(r["id"]), State: text(r["state"]), Sender: text(r["sender"]), Body: text(r["body"]), CreatedAt: text(r["created_at"]), NotificationState: text(r["notification_state"]), Parts: integer(r["parts"])}
	var decoded struct {
		Timestamp string `json:"timestamp"`
	}
	if json.Unmarshal([]byte(text(r["first_decoded"])), &decoded) == nil {
		if _, e := time.Parse(time.RFC3339, decoded.Timestamp); e == nil {
			m.SentAt = decoded.Timestamp
		}
	}
	var meta struct {
		SIMID     string `json:"sim_id"`
		SIMNumber string `json:"sim_number"`
	}
	if json.Unmarshal([]byte(text(r["first_metadata"])), &meta) == nil {
		m.SIMID, m.SIMNumber = meta.SIMID, meta.SIMNumber
	}
	return m
}
func (s *Store) Messages(search string, limit, offset int) ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.guard(); e != nil {
		return nil, e
	}
	if len(search) > 1024 || limit < 1 || limit > 200 || offset < 0 {
		return nil, errors.New("invalid history query")
	}
	// Literal substring search: SQL wildcard characters have no special meaning.
	rows, e := s.db.Query(`SELECT m.*,COUNT(p.ordinal) AS parts,(SELECT r.decoded_json FROM message_parts q JOIN receipts r ON r.id=q.receipt_id WHERE q.message_id=m.id ORDER BY q.ordinal LIMIT 1) AS first_decoded,(SELECT r.metadata_json FROM message_parts q JOIN receipts r ON r.id=q.receipt_id WHERE q.message_id=m.id ORDER BY q.ordinal LIMIT 1) AS first_metadata FROM messages m JOIN message_parts p ON p.message_id=m.id WHERE NOT EXISTS(SELECT 1 FROM message_visibility v WHERE v.message_id=m.id) AND (?='' OR instr(lower(coalesce(m.sender,'')),lower(?))>0 OR instr(lower(coalesce(m.body,'')),lower(?))>0) GROUP BY m.id ORDER BY m.created_at DESC,m.id LIMIT ? OFFSET ?`, search, search, search, limit, offset)
	if e != nil {
		return nil, e
	}
	out := []Message{}
	for _, r := range rows {
		m := messageRow(r)
		runes := []rune(m.Body)
		if len(runes) > 300 {
			m.Body = string(runes[:300])
		}
		out = append(out, m)
	}
	return out, nil
}
func (s *Store) SetDecoded(rid string, decoded M) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, e := s.verify(rid); e != nil {
		return e
	}
	switch text(decoded["state"]) {
	case "readable", "fragment", "binary", "decode_error", "unknown":
	default:
		return errors.New("invalid decode state")
	}
	row, e := s.receipt(rid)
	if e != nil {
		return e
	}
	d, e := encoded(decoded)
	if e != nil {
		return e
	}
	if row["decoded_json"] == d {
		return nil
	}
	return s.event("decoded", rid, decoded)
}

// Materialize deduplicates identical PDU sets across snapshots, slots and restarts.
// No receipt or raw occurrence is removed, including repeated network deliveries.
func (s *Store) Materialize(parts []string, state, sender, body string, notify bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(parts) == 0 || len(parts) > 255 {
		return "", errors.New("invalid message part count")
	}
	switch state {
	case "complete", "incomplete", "binary", "decode_error":
	default:
		return "", errors.New("invalid message state")
	}
	seen := map[string]bool{}
	hashes := []string{}
	for _, id := range parts {
		if seen[id] {
			return "", errors.New("repeated message part")
		}
		seen[id] = true
		if _, e := s.verify(id); e != nil {
			return "", e
		}
		r, e := s.receipt(id)
		if e != nil {
			return "", e
		}
		if r["archive_state"] != "reconciled" {
			return "", errors.New("message before reconcile")
		}
		hashes = append(hashes, text(r["pdu_ascii_sha256"]))
	}
	sort.Strings(hashes)
	identity, _ := Canonical(M{"pdu_hashes": hashes, "state": state})
	mid := Hash(identity)
	// Compatibility with historical message identifiers based on receipt IDs.
	rows, e := s.db.Query(`SELECT m.id,r.pdu_ascii_sha256 FROM messages m JOIN message_parts p ON p.message_id=m.id JOIN receipts r ON r.id=p.receipt_id WHERE m.state=?`, state)
	if e != nil {
		return "", e
	}
	previous := map[string][]string{}
	for _, r := range rows {
		key := text(r["id"])
		previous[key] = append(previous[key], text(r["pdu_ascii_sha256"]))
	}
	for id, hs := range previous {
		sort.Strings(hs)
		if strings.Join(hs, ",") == strings.Join(hashes, ",") {
			return id, nil
		}
	}
	return mid, s.event("message_materialized", "", M{"id": mid, "parts": parts, "state": state, "sender": nullable(sender), "body": nullable(body), "notify": notify && state == "complete" && body != ""})
}
func (s *Store) PendingNotifications() ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.guard(); e != nil {
		return nil, e
	}
	rows, e := s.db.Query(`SELECT m.*,COUNT(p.ordinal) AS parts,(SELECT r.decoded_json FROM message_parts q JOIN receipts r ON r.id=q.receipt_id WHERE q.message_id=m.id ORDER BY q.ordinal LIMIT 1) AS first_decoded,(SELECT r.metadata_json FROM message_parts q JOIN receipts r ON r.id=q.receipt_id WHERE q.message_id=m.id ORDER BY q.ordinal LIMIT 1) AS first_metadata FROM messages m JOIN notifications n ON n.message_id=m.id JOIN message_parts p ON p.message_id=m.id WHERE n.state='pending' AND NOT EXISTS(SELECT 1 FROM message_visibility v WHERE v.message_id=m.id) GROUP BY m.id ORDER BY m.created_at LIMIT 32`)
	if e != nil {
		return nil, e
	}
	out := []Message{}
	for _, r := range rows {
		parts, e := s.db.Query("SELECT receipt_id FROM message_parts WHERE message_id=?", r["id"])
		if e != nil {
			return nil, e
		}
		for _, p := range parts {
			if _, e = s.verify(text(p["receipt_id"])); e != nil {
				return nil, e
			}
		}
		out = append(out, messageRow(r))
	}
	return out, nil
}
func (s *Store) NotificationResult(id, state, detail string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch state {
	case "submitted", "failed", "denied", "disabled":
	default:
		return errors.New("invalid notification result")
	}
	if len(detail) > 1024 {
		return errors.New("notification detail too long")
	}
	rows, e := s.db.Query("SELECT state FROM notifications WHERE message_id=?", id)
	if e != nil {
		return e
	}
	if len(rows) != 1 {
		return errors.New("notification not found")
	}
	if rows[0]["state"] != "pending" {
		return nil
	}
	return s.event("notification_result", "", M{"id": id, "state": state, "error": nullable(detail)})
}

// FragmentCandidates returns one verified occurrence per PDU; completed multipart
// messages are excluded so reference reuse cannot combine with completed history.
func (s *Store) FragmentCandidates() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := s.db.Query(`SELECT r.id,r.pdu_ascii_sha256 FROM receipts r WHERE r.decode_state='fragment' AND NOT EXISTS(SELECT 1 FROM message_parts p JOIN messages m ON m.id=p.message_id JOIN receipts x ON x.id=p.receipt_id WHERE x.pdu_ascii_sha256=r.pdu_ascii_sha256 AND m.state='complete') ORDER BY r.archived_at DESC`)
	if e != nil {
		return nil, e
	}
	seen := map[string]bool{}
	out := []string{}
	for _, r := range rows {
		h := text(r["pdu_ascii_sha256"])
		if !seen[h] {
			id := text(r["id"])
			if _, e = s.verify(id); e != nil {
				return nil, e
			}
			out = append(out, id)
			seen[h] = true
		}
	}
	return out, nil
}

func (s *Store) LatestEvent(kind string) (M, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.guard(); e != nil {
		return nil, e
	}
	rows, e := s.db.Query("SELECT payload FROM events WHERE kind=? ORDER BY seq DESC LIMIT 1", kind)
	if e != nil || len(rows) == 0 {
		return nil, e
	}
	var out M
	e = Decode([]byte(text(rows[0]["payload"])), &out)
	return out, e
}
func (s *Store) Undecoded() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := s.db.Query("SELECT id FROM receipts WHERE decode_state='unknown' ORDER BY archived_at")
	if e != nil {
		return nil, e
	}
	out := []string{}
	for _, r := range rows {
		out = append(out, text(r["id"]))
	}
	return out, nil
}

// ConsolidateHistory changes only the journal-backed display projection. Every
// receipt and every original message-materialization event remains permanent.
func (s *Store) ConsolidateHistory() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := s.db.Query(`SELECT m.id,m.state,r.pdu_ascii_sha256 FROM messages m JOIN message_parts p ON p.message_id=m.id JOIN receipts r ON r.id=p.receipt_id ORDER BY m.created_at,m.id,p.ordinal`)
	if e != nil {
		return e
	}
	type group struct {
		id, state string
		hashes    []string
	}
	groups := []*group{}
	byID := map[string]*group{}
	hidden, e := s.db.Query("SELECT message_id FROM message_visibility")
	if e != nil {
		return e
	}
	already := map[string]bool{}
	for _, r := range hidden {
		already[text(r["message_id"])] = true
	}
	for _, r := range rows {
		id := text(r["id"])
		g := byID[id]
		if g == nil {
			g = &group{id: id, state: text(r["state"])}
			byID[id] = g
			groups = append(groups, g)
		}
		g.hashes = append(g.hashes, text(r["pdu_ascii_sha256"]))
	}
	completed := []map[string]bool{}
	for _, g := range groups {
		if g.state == "complete" {
			set := map[string]bool{}
			for _, h := range g.hashes {
				set[h] = true
			}
			completed = append(completed, set)
		}
	}
	seen := map[string]bool{}
	for _, g := range groups {
		sort.Strings(g.hashes)
		key := g.state + "/" + strings.Join(g.hashes, ",")
		reason := ""
		if seen[key] {
			reason = "identical PDU set already displayed"
		}
		seen[key] = true
		if g.state == "incomplete" {
			for _, full := range completed {
				contained := true
				for _, h := range g.hashes {
					contained = contained && full[h]
				}
				if contained {
					reason = "all fragments represented by complete message"
					break
				}
			}
		}
		if reason != "" && !already[g.id] {
			if e = s.event("message_hidden", "", M{"id": g.id, "reason": reason}); e != nil {
				return e
			}
		}
	}
	return nil
}

func (s *Store) Message(id string) (Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(id) != 64 {
		return Message{}, errors.New("invalid message identity")
	}
	rows, e := s.db.Query(`SELECT m.*,COUNT(p.ordinal) AS parts,(SELECT r.decoded_json FROM message_parts q JOIN receipts r ON r.id=q.receipt_id WHERE q.message_id=m.id ORDER BY q.ordinal LIMIT 1) AS first_decoded,(SELECT r.metadata_json FROM message_parts q JOIN receipts r ON r.id=q.receipt_id WHERE q.message_id=m.id ORDER BY q.ordinal LIMIT 1) AS first_metadata FROM messages m JOIN message_parts p ON p.message_id=m.id WHERE m.id=? GROUP BY m.id`, id)
	if e != nil {
		return Message{}, e
	}
	if len(rows) != 1 {
		return Message{}, errors.New("message not found")
	}
	parts, e := s.db.Query("SELECT receipt_id FROM message_parts WHERE message_id=?", id)
	if e != nil {
		return Message{}, e
	}
	for _, p := range parts {
		if _, e = s.verify(text(p["receipt_id"])); e != nil {
			return Message{}, e
		}
	}
	return messageRow(rows[0]), nil
}
