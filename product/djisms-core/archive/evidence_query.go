package archive

import (
	"errors"
	"path/filepath"
	"strings"
)

type SourceFact struct {
	Seq      int    `json:"seq"`
	Name     string `json:"name"`
	Hash     string `json:"sha256"`
	Observed string `json:"observed_utc"`
	Data     []byte `json:"-"`
}

func (s *Store) SourceFacts(prefix string) ([]SourceFact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.guard(); e != nil {
		return nil, e
	}
	if prefix == "" || len(prefix) > 4096 {
		return nil, errors.New("bounded literal source prefix required")
	}
	rows, e := s.db.Query("SELECT seq,occurred_at,payload FROM events WHERE kind='source_preserved' AND substr(json_extract(payload,'$.name'),1,?)=? ORDER BY seq", len(prefix), prefix)
	if e != nil {
		return nil, e
	}
	out := []SourceFact{}
	for _, r := range rows {
		var m M
		if e = Decode([]byte(text(r["payload"])), &m); e != nil {
			return nil, e
		}
		h := text(m["sha256"])
		if len(h) != 64 || strings.Trim(h, "0123456789abcdef") != "" {
			return nil, errors.New("invalid source hash")
		}
		b, e := Read(filepath.Join(s.Root, "sources", h+".bin"))
		if e != nil {
			return nil, e
		}
		if Hash(b) != h {
			return nil, errors.New("source mutation")
		}
		out = append(out, SourceFact{integer(r["seq"]), text(m["name"]), h, text(r["occurred_at"]), b})
	}
	return out, nil
}
func (s *Store) EventsOfKind(kind string) ([]M, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.guard(); e != nil {
		return nil, e
	}
	rows, e := s.db.Query("SELECT payload FROM events WHERE kind=? ORDER BY seq", kind)
	if e != nil {
		return nil, e
	}
	out := []M{}
	for _, r := range rows {
		var m M
		if e = Decode([]byte(text(r["payload"])), &m); e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, nil
}

// StatusRecoveryContext binds review to the final query in the status session
// immediately preceding the latest STOP, never to an arbitrary old report.
func (s *Store) StatusRecoveryContext(session, reportHash, hostHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.guard(); e != nil {
		return e
	}
	if len(session) != 32 || strings.Trim(session, "0123456789abcdef") != "" {
		return errors.New("invalid session")
	}
	rows, e := s.db.Query("SELECT seq FROM events WHERE kind='core_safety_stop' ORDER BY seq DESC LIMIT 1")
	if e != nil {
		return e
	}
	if len(rows) != 1 {
		return errors.New("missing STOP")
	}
	seq := integer(rows[0]["seq"])
	rows, e = s.db.Query("SELECT payload FROM events WHERE kind='source_preserved' AND seq<? ORDER BY seq DESC LIMIT 1", seq)
	if e != nil {
		return e
	}
	if len(rows) != 1 {
		return errors.New("missing source")
	}
	var m M
	if e = Decode([]byte(text(rows[0]["payload"])), &m); e != nil {
		return e
	}
	prefix := "session/" + session + "/"
	if !strings.HasPrefix(text(m["name"]), prefix) {
		return errors.New("STOP belongs to another session")
	}
	for suffix, want := range map[string]string{"status_query": reportHash, "host_before": hostHash} {
		rows, e = s.db.Query("SELECT payload FROM events WHERE kind='source_preserved' AND seq<? AND json_extract(payload,'$.name') LIKE ? ORDER BY seq DESC LIMIT 1", seq, prefix+"%/"+suffix)
		if e != nil {
			return e
		}
		if len(rows) != 1 {
			return errors.New("missing status provenance")
		}
		if e = Decode([]byte(text(rows[0]["payload"])), &m); e != nil {
			return e
		}
		if m["sha256"] != want {
			return errors.New("stale status recovery evidence")
		}
	}
	return nil
}
