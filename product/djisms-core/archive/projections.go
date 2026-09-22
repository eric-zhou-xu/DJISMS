package archive

import (
	"errors"
	"fmt"
)

// Verify every lifecycle/query table against the already verified immutable
// journal. A modified SQLite status must never erase an unknown delete attempt.
func (s *Store) verifyProjections() error {
	tables := map[string]map[string]M{"messages": {}, "message_parts": {}, "notifications": {}, "purge_attempts": {}, "message_visibility": {}}
	rows, e := s.db.Query(`SELECT kind,record_id,occurred_at,payload FROM events WHERE kind IN ('message_materialized','message_hidden','notification_result','delete_intent','delete_result') ORDER BY seq`)
	if e != nil {
		return e
	}
	for _, r := range rows {
		var d M
		if e = Decode([]byte(text(r["payload"])), &d); e != nil {
			return e
		}
		when := r["occurred_at"]
		switch r["kind"] {
		case "message_materialized":
			id := text(d["id"])
			state := "suppressed"
			if d["notify"] == true {
				state = "pending"
			}
			tables["messages"][id] = M{"id": id, "state": d["state"], "sender": d["sender"], "body": d["body"], "created_at": when, "notification_state": state}
			tables["notifications"][id] = M{"message_id": id, "state": state, "attempts": 0, "last_error": nil, "updated_at": when}
			parts, _ := d["parts"].([]any)
			for i, p := range parts {
				key := fmt.Sprintf("%s/%d", id, i+1)
				tables["message_parts"][key] = M{"message_id": id, "receipt_id": p, "ordinal": i + 1}
			}
		case "notification_result":
			id := text(d["id"])
			n := tables["notifications"][id]
			m := tables["messages"][id]
			if n == nil || m == nil {
				return errors.New("notification journal has no message")
			}
			n["state"] = d["state"]
			n["attempts"] = integer(n["attempts"]) + 1
			n["last_error"] = d["error"]
			n["updated_at"] = when
			m["notification_state"] = d["state"]
		case "message_hidden":
			id := text(d["id"])
			if tables["message_visibility"][id] == nil {
				tables["message_visibility"][id] = M{"message_id": id, "reason": d["reason"], "hidden_at": when}
			}
		case "delete_intent":
			id := text(d["attempt_id"])
			tables["purge_attempts"][id] = M{"id": id, "receipt_id": r["record_id"], "storage": d["storage"], "storage_index": integer(d["index"]), "expected_pdu_sha256": d["pdu_sha256"], "connection_id": d["connection_id"], "mode": d["mode"], "state": "intent", "response": nil, "started_at": when, "updated_at": when}
		case "delete_result":
			id := text(d["attempt_id"])
			p := tables["purge_attempts"][id]
			if p == nil {
				return errors.New("delete journal result without intent")
			}
			p["state"] = d["state"]
			p["response"] = d["response"]
			p["updated_at"] = when
		}
	}
	for table, expected := range tables {
		actual, e := s.db.Query("SELECT * FROM " + table)
		if e != nil {
			return e
		}
		if len(actual) != len(expected) {
			return fmt.Errorf("%s projection count differs", table)
		}
		for _, r := range actual {
			key := text(r["id"])
			if table == "notifications" || table == "message_visibility" {
				key = text(r["message_id"])
			}
			if table == "message_parts" {
				key = fmt.Sprintf("%s/%d", text(r["message_id"]), integer(r["ordinal"]))
			}
			want, ok := expected[key]
			if !ok {
				return fmt.Errorf("%s projection row lacks proof", table)
			}
			a, e := Canonical(r)
			if e != nil {
				return e
			}
			b, e := Canonical(want)
			if e != nil {
				return e
			}
			if string(a) != string(b) {
				return fmt.Errorf("%s projection differs from journal", table)
			}
		}
	}
	return nil
}
