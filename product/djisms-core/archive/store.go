// Package archive owns durable raw facts, an immutable journal and its SQLite projection.
package archive

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/iniwex5/vohive/product/djisms-core/database"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed schema.sql
var schema string

type M = map[string]any
type Store struct {
	mu       sync.Mutex
	Root     string
	db       *database.DB
	lock     *os.File
	seq      int
	head     string
	proof    map[string][]M
	poisoned bool
	Fault    func(string) error
}

func text(v any) string { s, _ := v.(string); return s }
func integer(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		i, e := n.Int64()
		if e == nil {
			return int(i)
		}
	}
	return -1
}
func object(v any) M { m, _ := v.(map[string]any); return m }
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func encoded(v any) (string, error) { b, e := Canonical(v); return string(b), e }
func now() string                   { return time.Now().UTC().Format(time.RFC3339Nano) }
func DefaultRoot() (string, error) {
	home, e := os.UserHomeDir()
	if e != nil {
		return "", e
	}
	return filepath.Join(home, "Library", "Application Support", "DJISMS"), nil
}
func Open(root string) (s *Store, err error) {
	if os.Geteuid() == 0 {
		return nil, errors.New("DJISMS runs as the current ordinary user")
	}
	if root == "" {
		root, err = DefaultRoot()
		if err != nil {
			return nil, err
		}
	}
	if err = PrivateDir(root); err != nil {
		return nil, err
	}
	s = &Store{Root: root, head: strings.Repeat("0", 64), proof: map[string][]M{}}
	defer func() {
		if err != nil {
			s.Close()
			s = nil
		}
	}()
	for _, d := range []string{"raw", "journal", "sources", "plans", "recovery", "acquisition"} {
		if err = PrivateDir(filepath.Join(root, d)); err != nil {
			return s, err
		}
	}
	fd, e := syscall.Open(filepath.Join(root, ".writer.lock"), syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return s, e
	}
	s.lock = os.NewFile(uintptr(fd), "archive-lock")
	if e = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return s, errors.New("archive is already owned by another DJISMS process")
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		p := filepath.Join(root, "inbox.sqlite3"+suffix)
		if _, e := os.Lstat(p); e == nil {
			if _, e = Read(p); e != nil {
				return s, e
			}
		} else if !os.IsNotExist(e) {
			return s, e
		}
	}
	// Pre-create with owner-only permissions before SQLite can publish sidecars.
	path := filepath.Join(root, "inbox.sqlite3")
	if _, e = os.Stat(path); os.IsNotExist(e) {
		f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return s, e
		}
		if e = errors.Join(FullSync(f), f.Close()); e != nil {
			return s, e
		}
	}
	s.db, err = database.Open(path)
	if err != nil {
		return s, err
	}
	if err = s.db.Script("PRAGMA foreign_keys=ON; PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA fullfsync=ON; PRAGMA checkpoint_fullfsync=ON; PRAGMA busy_timeout=3000;" + schema); err != nil {
		return s, err
	}
	for _, q := range []string{"PRAGMA quick_check", "PRAGMA integrity_check"} {
		rows, e := s.db.Query(q)
		if e != nil || len(rows) != 1 {
			return s, errors.New("SQLite integrity check failed")
		}
		for _, v := range rows[0] {
			if v != "ok" {
				return s, errors.New("SQLite integrity check failed")
			}
		}
	}
	rows, e := s.db.Query("SELECT version FROM schema_version")
	if e != nil || len(rows) != 1 || integer(rows[0]["version"]) != 1 {
		return s, errors.New("unsupported schema")
	}
	if err = s.recover(); err != nil {
		return s, err
	}
	if err = s.retainPublishedPending(); err != nil {
		return s, err
	}
	return s, SyncDir(root)
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var e error
	if s.db != nil {
		e = s.db.Close()
		s.db = nil
	}
	if s.lock != nil {
		e = errors.Join(e, s.lock.Close())
		s.lock = nil
	}
	return e
}
func (s *Store) guard() error {
	if s.poisoned || s.db == nil {
		return errors.New("archive unavailable; recovery required")
	}
	return nil
}
func (s *Store) fault(point string) error {
	if s.Fault != nil {
		return s.Fault(point)
	}
	return nil
}
func (s *Store) event(kind, rid string, data M) error {
	if e := s.guard(); e != nil {
		return e
	}
	event := M{"schema": 1, "seq": s.seq + 1, "previous": s.head, "kind": kind, "record_id": nullable(rid), "time": now(), "data": data}
	b, e := Canonical(event)
	if e != nil {
		return e
	}
	hash := Hash(b)
	event["hash"] = hash
	b, e = Canonical(event)
	if e != nil {
		return e
	}
	if e = WriteNew(filepath.Join(s.Root, "journal", fmt.Sprintf("%012d.json", s.seq+1)), b); e != nil {
		s.poisoned = true
		return e
	}
	s.seq++
	s.head = hash
	if e = s.fault("journal_durable:" + kind); e == nil {
		e = s.project(event)
	}
	if e != nil {
		s.poisoned = true
		return e
	}
	if rid != "" {
		s.proof[rid] = append(s.proof[rid], event)
	}
	return nil
}
func (s *Store) Event(kind, rid string, data M) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.event(kind, rid, data)
}
func (s *Store) project(e M) (err error) {
	if err = s.db.Exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = s.db.Exec("ROLLBACK")
		}
	}()
	d := object(e["data"])
	rid := e["record_id"]
	kind := text(e["kind"])
	payload, err := encoded(d)
	if err != nil {
		return err
	}
	switch kind {
	case "raw_preserved":
		m := object(d["metadata"])
		meta, x := encoded(m)
		if x != nil {
			return x
		}
		var idx any
		if m["index"] != nil {
			idx = integer(m["index"])
		}
		err = s.db.Exec(`INSERT INTO receipts(id,source_id,device_key,connection_id,storage,storage_index,raw_path,raw_file_sha256,pdu_ascii_sha256,pdu_bytes_sha256,metadata_json,archived_at,archive_state) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,'preserved')`, rid, d["source_id"], m["device_key"], m["connection_id"], m["storage"], idx, d["raw_path"], d["raw_sha256"], d["pdu_ascii_sha256"], d["pdu_bytes_sha256"], meta, d["archived_at"])
	case "archive_verified", "archive_reconciled":
		state := "verified"
		if kind == "archive_reconciled" {
			state = "reconciled"
		}
		err = s.db.Exec("UPDATE receipts SET archive_state=? WHERE id=?", state, rid)
	case "decoded":
		err = s.db.Exec("UPDATE receipts SET decode_state=?,sender=?,body=?,decoded_json=? WHERE id=?", d["state"], d["sender"], d["text"], payload, rid)
	case "message_materialized":
		state := "suppressed"
		if d["notify"] == true {
			state = "pending"
		}
		err = s.db.Exec("INSERT INTO messages VALUES(?,?,?,?,?,?)", d["id"], d["state"], d["sender"], d["body"], e["time"], state)
		if err != nil {
			return err
		}
		parts, ok := d["parts"].([]any)
		if !ok {
			if ss, yes := d["parts"].([]string); yes {
				for _, p := range ss {
					parts = append(parts, p)
				}
			}
		}
		if len(parts) == 0 {
			return errors.New("message without parts")
		}
		for n, p := range parts {
			if err = s.db.Exec("INSERT INTO message_parts VALUES(?,?,?)", d["id"], p, n+1); err != nil {
				return err
			}
		}
		err = s.db.Exec("INSERT INTO notifications VALUES(?,?,0,NULL,?)", d["id"], state, e["time"])
	case "message_hidden":
		err = s.db.Exec("INSERT OR IGNORE INTO message_visibility VALUES(?,?,?)", d["id"], d["reason"], e["time"])
	case "notification_result":
		err = s.db.Exec("UPDATE notifications SET state=?,attempts=attempts+1,last_error=?,updated_at=? WHERE message_id=?", d["state"], d["error"], e["time"], d["id"])
		if err == nil {
			err = s.db.Exec("UPDATE messages SET notification_state=? WHERE id=?", d["state"], d["id"])
		}
	case "delete_intent":
		err = s.db.Exec("INSERT INTO purge_attempts VALUES(?,?,?,?,?,?,?,?,NULL,?,?)", d["attempt_id"], rid, d["storage"], integer(d["index"]), d["pdu_sha256"], d["connection_id"], d["mode"], "intent", e["time"], e["time"])
		if err == nil {
			err = s.db.Exec("UPDATE receipts SET delete_state='intent' WHERE id=?", rid)
		}
	case "delete_result":
		err = s.db.Exec("UPDATE purge_attempts SET state=?,response=?,updated_at=? WHERE id=?", d["state"], d["response"], e["time"], d["attempt_id"])
		if err == nil {
			err = s.db.Exec("UPDATE receipts SET delete_state=? WHERE id=?", d["state"], rid)
		}
	}
	if err != nil {
		return err
	}
	if err = s.db.Exec("INSERT INTO events VALUES(?,?,?,?,?,?)", integer(e["seq"]), e["hash"], kind, rid, e["time"], payload); err != nil {
		return err
	}
	if err = s.fault("projection_uncommitted:" + kind); err != nil {
		return err
	}
	if err = s.db.Exec("COMMIT"); err != nil {
		return err
	}
	return s.fault("projection_committed:" + kind)
}
func (s *Store) recover() error {
	paths, e := filepath.Glob(filepath.Join(s.Root, "journal", "*.json"))
	if e != nil {
		return e
	}
	sort.Strings(paths)
	for _, p := range paths {
		b, e := Read(p)
		if e != nil {
			return e
		}
		var event M
		if e = Decode(b, &event); e != nil {
			return e
		}
		hash := text(event["hash"])
		delete(event, "hash")
		raw, e := Canonical(event)
		if e != nil {
			return e
		}
		if integer(event["schema"]) != 1 || integer(event["seq"]) != s.seq+1 || event["previous"] != s.head || hash != Hash(raw) || filepath.Base(p) != fmt.Sprintf("%012d.json", s.seq+1) {
			return errors.New("journal chain mismatch")
		}
		event["hash"] = hash
		s.seq++
		s.head = hash
		rows, e := s.db.Query("SELECT * FROM events WHERE seq=?", s.seq)
		if e != nil {
			return e
		}
		if len(rows) == 0 {
			if e = s.project(event); e != nil {
				return e
			}
		} else {
			payload, e := encoded(event["data"])
			if e != nil {
				return e
			}
			r := rows[0]
			if r["event_hash"] != hash || r["kind"] != event["kind"] || r["record_id"] != event["record_id"] || r["occurred_at"] != event["time"] || r["payload"] != payload {
				return errors.New("SQLite/journal disagreement")
			}
		}
		rid := text(event["record_id"])
		if rid != "" {
			s.proof[rid] = append(s.proof[rid], event)
		}
	}
	rows, e := s.db.Query("SELECT COALESCE(MAX(seq),0) AS n FROM events")
	if e != nil {
		return e
	}
	if integer(rows[0]["n"]) != s.seq {
		return errors.New("missing journal events")
	}
	paths, e = filepath.Glob(filepath.Join(s.Root, "raw", "*.json"))
	if e != nil {
		return e
	}
	for _, p := range paths {
		b, e := Read(p)
		if e != nil {
			return e
		}
		var env M
		if e = Decode(b, &env); e != nil {
			return e
		}
		if e = validateEnvelope(env); e != nil {
			return e
		}
		rid := text(env["id"])
		if filepath.Base(p) != rid+".json" {
			return errors.New("raw path mismatch")
		}
		rows, e = s.db.Query("SELECT id FROM receipts WHERE id=?", rid)
		if e != nil {
			return e
		}
		if len(rows) == 0 {
			if e = s.register(env, b); e != nil {
				return e
			}
		}
	}
	rows, e = s.db.Query("SELECT id FROM receipts")
	if e != nil {
		return e
	}
	for _, r := range rows {
		if _, e = s.verify(text(r["id"])); e != nil {
			return e
		}
	}
	sources, e := s.db.Query("SELECT payload FROM events WHERE kind='source_preserved'")
	if e != nil {
		return e
	}
	for _, r := range sources {
		var d M
		if e = Decode([]byte(text(r["payload"])), &d); e != nil {
			return e
		}
		h := text(d["sha256"])
		if len(h) != 64 {
			return errors.New("source digest invalid")
		}
		b, e := Read(filepath.Join(s.Root, "sources", h+".bin"))
		if e != nil || Hash(b) != h {
			return errors.New("source archive missing or changed")
		}
	}
	return s.verifyProjections()
}
func validateEnvelope(e M) error {
	if integer(e["schema"]) != 1 || text(e["source_id"]) == "" || Hash([]byte(text(e["source_id"]))) != e["id"] {
		return errors.New("raw identity invalid")
	}
	m := object(e["metadata"])
	if text(m["device_key"]) == "" || text(m["connection_id"]) == "" || text(m["received_at"]) == "" {
		return errors.New("raw metadata incomplete")
	}
	if m["index"] != nil {
		if integer(m["index"]) < 0 || integer(m["index"]) > 65535 || text(m["storage"]) == "" {
			return errors.New("raw coordinates invalid")
		}
	} else if m["storage"] != nil {
		return errors.New("direct SMS cannot invent a slot")
	}
	if _, ok := e["raw_pdu"].(string); !ok {
		return errors.New("PDU must preserve exact string")
	}
	return nil
}
func (s *Store) register(env M, b []byte) error {
	asc, bin := PDUHashes(text(env["raw_pdu"]))
	return s.event("raw_preserved", text(env["id"]), M{"source_id": env["source_id"], "metadata": env["metadata"], "archived_at": env["archived_at"], "raw_path": "raw/" + text(env["id"]) + ".json", "raw_sha256": Hash(b), "pdu_ascii_sha256": asc, "pdu_bytes_sha256": nullable(bin)})
}
func (s *Store) receipt(rid string) (database.Row, error) {
	r, e := s.db.Query("SELECT * FROM receipts WHERE id=?", rid)
	if e != nil {
		return nil, e
	}
	if len(r) != 1 {
		return nil, errors.New("unknown receipt")
	}
	return r[0], nil
}
func (s *Store) verify(rid string) (M, error) {
	row, e := s.receipt(rid)
	if e != nil {
		return nil, e
	}
	if row["raw_path"] != "raw/"+rid+".json" {
		return nil, errors.New("unsafe raw path")
	}
	b, e := Read(filepath.Join(s.Root, text(row["raw_path"])))
	if e != nil {
		return nil, e
	}
	if Hash(b) != row["raw_file_sha256"] {
		return nil, errors.New("raw file hash changed")
	}
	var env M
	if e = Decode(b, &env); e != nil {
		return nil, e
	}
	if e = validateEnvelope(env); e != nil {
		return nil, e
	}
	asc, bin := PDUHashes(text(env["raw_pdu"]))
	m := object(env["metadata"])
	meta, e := encoded(m)
	if e != nil {
		return nil, e
	}
	if env["id"] != rid || asc != row["pdu_ascii_sha256"] || nullable(bin) != row["pdu_bytes_sha256"] || meta != row["metadata_json"] {
		return nil, errors.New("raw/SQLite metadata mismatch")
	}
	for a, b := range map[string]string{"device_key": "device_key", "connection_id": "connection_id", "storage": "storage"} {
		if row[a] != m[b] {
			return nil, errors.New("SQLite coordinates differ")
		}
	}
	if (row["storage_index"] == nil) != (m["index"] == nil) || integer(row["storage_index"]) != integer(m["index"]) {
		return nil, errors.New("SQLite index differs")
	}
	state, deleted := "preserved", "not_requested"
	var preserved, decoded M
	for _, ev := range s.proof[rid] {
		raw, e := Read(filepath.Join(s.Root, "journal", fmt.Sprintf("%012d.json", integer(ev["seq"]))))
		if e != nil {
			return nil, e
		}
		var durable M
		if e = Decode(raw, &durable); e != nil {
			return nil, e
		}
		hash := durable["hash"]
		delete(durable, "hash")
		canonical, e := Canonical(durable)
		if e != nil || hash != ev["hash"] || Hash(canonical) != hash {
			return nil, errors.New("durable receipt journal changed")
		}
		d := object(ev["data"])
		switch ev["kind"] {
		case "raw_preserved":
			preserved = d
		case "archive_verified":
			state = "verified"
		case "archive_reconciled":
			state = "reconciled"
		case "decoded":
			decoded = d
		case "delete_intent":
			deleted = "intent"
		case "delete_result":
			deleted = text(d["state"])
		}
	}
	if preserved == nil || preserved["raw_sha256"] != row["raw_file_sha256"] || preserved["source_id"] != row["source_id"] || state != row["archive_state"] || deleted != row["delete_state"] {
		return nil, errors.New("archive state lacks durable lifecycle proof")
	}
	if decoded != nil {
		d, e := encoded(decoded)
		if e != nil {
			return nil, e
		}
		if row["decoded_json"] != d || row["decode_state"] != decoded["state"] || row["sender"] != decoded["sender"] || row["body"] != decoded["text"] {
			return nil, errors.New("decoded projection differs")
		}
	} else if row["decode_state"] != "unknown" || row["decoded_json"] != nil {
		return nil, errors.New("decoded data lacks proof")
	}
	if state == "preserved" {
		if e = s.event("archive_verified", rid, M{"raw_sha256": row["raw_file_sha256"]}); e != nil {
			return nil, e
		}
	}
	return env, nil
}
func (s *Store) Verify(rid string) (M, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.verify(rid) }
func (s *Store) Receipt(rid string) (database.Row, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.receipt(rid)
}
func (s *Store) Ingest(source, pdu string, metadata, decoded M) (rid string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = s.guard(); err != nil {
		return
	}
	defer func() {
		if err != nil {
			s.poisoned = true
		}
	}()
	rid = Hash([]byte(source))
	path := filepath.Join(s.Root, "raw", rid+".json")
	env := M{"schema": 1, "id": rid, "source_id": source, "archived_at": now(), "raw_pdu": pdu, "metadata": metadata}
	if err = validateEnvelope(env); err != nil {
		return
	}
	b, e := Read(path)
	if e == nil {
		var old M
		if err = Decode(b, &old); err != nil {
			return
		}
		meta, _ := encoded(metadata)
		oldMeta, _ := encoded(old["metadata"])
		if old["source_id"] != source || old["raw_pdu"] != pdu || meta != oldMeta {
			err = errors.New("source reused with different raw fact")
			return
		}
		env = old
	} else if os.IsNotExist(e) {
		b, err = Canonical(env)
		if err != nil {
			return
		}
		if err = WriteNew(path, b); err != nil {
			return
		}
		if err = s.fault("raw_durable"); err != nil {
			return
		}
	} else {
		err = e
		return
	}
	rows, e := s.db.Query("SELECT id FROM receipts WHERE id=?", rid)
	if e != nil {
		err = e
		return
	}
	if len(rows) == 0 {
		if err = s.register(env, b); err != nil {
			return
		}
	}
	if _, err = s.verify(rid); err != nil {
		return
	}
	if decoded != nil {
		switch text(decoded["state"]) {
		case "readable", "fragment", "binary", "decode_error", "unknown":
		default:
			err = errors.New("explicit decode state required")
			return
		}
		row, e := s.receipt(rid)
		if e != nil {
			err = e
			return
		}
		d, _ := encoded(decoded)
		if row["decoded_json"] != d {
			if err = s.event("decoded", rid, decoded); err != nil {
				return
			}
		}
	}
	row, e := s.receipt(rid)
	if e != nil {
		err = e
		return
	}
	if row["archive_state"] != "reconciled" {
		err = s.event("archive_reconciled", rid, M{"raw_sha256": row["raw_file_sha256"], "index_verified": true})
	}
	return
}
func (s *Store) Eligible(rid string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, e := s.verify(rid); e != nil {
		return false, e
	}
	r, e := s.receipt(rid)
	if e != nil {
		return false, e
	}
	return r["archive_state"] == "reconciled" && r["storage"] != nil && r["storage_index"] != nil && r["pdu_bytes_sha256"] != nil && r["decode_state"] != "unknown" && r["decode_state"] != "decode_error" && r["delete_state"] == "not_requested", nil
}
func (s *Store) SaveSource(name string, b []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.guard(); e != nil {
		return "", e
	}
	h := Hash(b)
	p := filepath.Join(s.Root, "sources", h+".bin")
	old, e := Read(p)
	if os.IsNotExist(e) {
		e = WriteNew(p, b)
	} else if e == nil && Hash(old) != h {
		e = errors.New("source changed")
	}
	if e != nil {
		return "", e
	}
	d := M{"name": name, "sha256": h}
	payload, _ := encoded(d)
	rows, e := s.db.Query("SELECT seq FROM events WHERE kind='source_preserved' AND payload=?", payload)
	if e != nil {
		return "", e
	}
	if len(rows) == 0 {
		e = s.event("source_preserved", "", d)
	}
	return h, e
}
func (s *Store) Summary() (M, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := M{"journal_events": s.seq, "journal_head": s.head}
	for k, q := range map[string]string{"receipts": "SELECT COUNT(*) AS n FROM receipts", "logical_messages": "SELECT COUNT(*) AS n FROM messages", "unfinished_delete_attempts": "SELECT COUNT(*) AS n FROM purge_attempts WHERE state IN ('intent','unknown')"} {
		r, e := s.db.Query(q)
		if e != nil {
			return nil, e
		}
		out[k] = integer(r[0]["n"])
	}
	pending := 0
	e := filepath.WalkDir(s.Root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if strings.HasPrefix(d.Name(), ".pending-") {
			pending++
		}
		return nil
	})
	out["pending_temp_files"] = pending
	return out, e
}
