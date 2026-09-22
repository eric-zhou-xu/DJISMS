package archive

import (
	"errors"
	"fmt"
	"strings"
)

// ClosedReceiveContext proves journal adjacency, not merely matching filenames.
// It is read-only and must be called by an exclusively owned, verified Store.
func (s *Store) ClosedReceiveContext(session, endHash, hostHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.guard(); e != nil {
		return e
	}
	if len(session) != 32 || strings.Trim(session, "0123456789abcdef") != "" {
		return errors.New("invalid session")
	}
	stops, e := s.db.Query("SELECT seq FROM events WHERE kind='core_safety_stop' ORDER BY seq DESC LIMIT 1")
	if e != nil {
		return e
	}
	if len(stops) != 1 {
		return errors.New("missing STOP")
	}
	stopSeq := integer(stops[0]["seq"])
	rows, e := s.db.Query("SELECT seq,kind,payload FROM events WHERE seq < ? ORDER BY seq DESC", stopSeq)
	if e != nil {
		return e
	}
	prefix := "session/" + session + "/"
	count := 0
	foundStart := false
	for _, row := range rows {
		var p M
		if e = Decode([]byte(text(row["payload"])), &p); e != nil {
			return e
		}
		name := text(p["name"])
		if text(row["kind"]) == "source_preserved" && strings.HasPrefix(name, prefix) {
			if count == 0 && (integer(row["seq"]) != stopSeq-1 || !strings.HasSuffix(name, "/session_end") || p["sha256"] != endHash) {
				return errors.New("STOP is not adjacent to reviewed session end")
			}
			count++
			if integer(row["seq"]) != stopSeq-count {
				return errors.New("interleaved receive evidence")
			}
			if name == prefix+"000001/out_intent" {
				foundStart = true
				break
			}
		} else {
			return errors.New("missing contiguous closed receive session")
		}
	}
	if count == 0 || !foundStart {
		return errors.New("missing receive context")
	}
	prior, e := s.db.Query("SELECT payload FROM events WHERE kind='source_preserved' AND seq < ? AND json_extract(payload,'$.name') LIKE 'session/%/host_after' ORDER BY seq DESC LIMIT 1", stopSeq-count)
	if e != nil {
		return e
	}
	if len(prior) != 1 {
		return errors.New("missing prior host proof")
	}
	var p M
	if e = Decode([]byte(text(prior[0]["payload"])), &p); e != nil {
		return e
	}
	if p["sha256"] != hostHash {
		return errors.New("host is not the preceding verified host")
	}
	// A failed authorization can leave only its immutable proof or preferences.
	later, e := s.db.Query("SELECT kind,payload FROM events WHERE seq > ? ORDER BY seq", stopSeq)
	if e != nil {
		return e
	}
	for _, row := range later {
		kind := text(row["kind"])
		var v M
		if e = Decode([]byte(text(row["payload"])), &v); e != nil {
			return e
		}
		if kind == "preferences" && v["auto_purge"] == false {
			continue
		}
		if kind == "source_preserved" && strings.HasPrefix(text(v["name"]), "dns_recovery/") {
			continue
		}
		return fmt.Errorf("activity after STOP prevents new authorization: %s", kind)
	}
	return nil
}
