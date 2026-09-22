package archive

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type PreservedSessionFact struct {
	Kind string
	Data []byte
}

// PreservedSession loads every ordered source fact from the verified journal
// projection, requires contiguous source sequence numbers and rechecks hashes.
func (s *Store) PreservedSession(session string) ([]PreservedSessionFact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.guard(); e != nil {
		return nil, e
	}
	if len(session) != 32 || strings.Trim(session, "0123456789abcdef") != "" {
		return nil, errors.New("invalid session")
	}
	prefix := "session/" + session + "/"
	rows, e := s.db.Query("SELECT payload FROM events WHERE kind='source_preserved' AND json_extract(payload,'$.name') LIKE ? ORDER BY seq", prefix+"%")
	if e != nil {
		return nil, e
	}
	out := []PreservedSessionFact{}
	for i, row := range rows {
		var p M
		if e = Decode([]byte(text(row["payload"])), &p); e != nil {
			return nil, e
		}
		name, h := text(p["name"]), text(p["sha256"])
		if !strings.HasPrefix(name, fmt.Sprintf("%s%06d/", prefix, i+1)) || len(h) != 64 || strings.Trim(h, "0123456789abcdef") != "" {
			return nil, errors.New("noncontiguous or invalid source evidence")
		}
		data, e := Read(filepath.Join(s.Root, "sources", h+".bin"))
		if e != nil {
			return nil, e
		}
		if Hash(data) != h {
			return nil, errors.New("changed source")
		}
		out = append(out, PreservedSessionFact{filepath.Base(name), data})
	}
	if len(out) == 0 {
		return nil, errors.New("empty session evidence")
	}
	return out, nil
}
