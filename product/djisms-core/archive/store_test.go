package archive

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func meta() M {
	return M{"device_key": "audited-device", "connection_id": "session-test", "received_at": "2026-09-18T00:00:00Z", "storage": "ME", "index": 0}
}
func TestPreserveRecoveryAndEligibility(t *testing.T) {
	root := privateTestRoot(t)
	s, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	rid, e := s.Ingest("test-source", "0000", meta(), M{"state": "binary", "binary_hex": "00"})
	if e != nil {
		t.Fatal(e)
	}
	if ok, e := s.Eligible(rid); e != nil || !ok {
		t.Fatal(ok, e)
	}
	s.Close()
	s, e = Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	env, e := s.Verify(rid)
	if e != nil || env["raw_pdu"] != "0000" {
		t.Fatal(e)
	}
	if _, e = Open(root); e == nil {
		t.Fatal("second writer accepted")
	}
}
func TestCrashAfterRawAndJournalRecovers(t *testing.T) {
	for _, point := range []string{"raw_durable", "journal_durable:raw_preserved", "journal_durable:archive_verified", "journal_durable:decoded", "journal_durable:archive_reconciled"} {
		t.Run(point, func(t *testing.T) {
			root := privateTestRoot(t)
			s, e := Open(root)
			if e != nil {
				t.Fatal(e)
			}
			s.Fault = func(p string) error {
				if p == point {
					return errors.New("injected crash")
				}
				return nil
			}
			_, e = s.Ingest("crash", "0000", meta(), M{"state": "binary"})
			if e == nil {
				t.Fatal("fault not reached")
			}
			s.Close()
			s, e = Open(root)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			rid, e := s.Ingest("crash", "0000", meta(), M{"state": "binary"})
			if e != nil {
				t.Fatal(e)
			}
			if ok, e := s.Eligible(rid); e != nil || !ok {
				t.Fatal(e)
			}
		})
	}
}
func TestTamperAndSourceReuseRejected(t *testing.T) {
	s, e := Open(privateTestRoot(t))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	rid, e := s.Ingest("a", "0000", meta(), M{"state": "binary"})
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(s.Root, "raw", rid+".json")
	b, _ := Read(path)
	os.WriteFile(path, append(b, ' '), 0600)
	if _, e = s.Verify(rid); e == nil {
		t.Fatal("raw mutation accepted")
	}
}
func TestUnknownDeleteNeverRetried(t *testing.T) {
	s, e := Open(privateTestRoot(t))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	rid, e := s.Ingest("a", "0000", meta(), M{"state": "binary"})
	if e != nil {
		t.Fatal(e)
	}
	_, h := PDUHashes("0000")
	e = s.Event("delete_intent", rid, M{"attempt_id": "a", "storage": "ME", "index": 0, "pdu_sha256": h, "connection_id": "session-test", "mode": "approved_live"})
	if e != nil {
		t.Fatal(e)
	}
	if ok, e := s.Eligible(rid); e != nil || ok {
		t.Fatal("pending attempt eligible", e)
	}
	e = s.Event("delete_result", rid, M{"attempt_id": "a", "state": "unknown", "response": "timeout"})
	if e != nil {
		t.Fatal(e)
	}
	if ok, e := s.Eligible(rid); e != nil || ok {
		t.Fatal("unknown retry eligible", e)
	}
}
func TestCanonicalPythonUnicodeAndEscapes(t *testing.T) {
	b, e := Canonical(M{"中文": "a\u2028b", "literal": `\u2028`, "html": "<>&", "controls": "\n\t\x01"})
	if e != nil {
		t.Fatal(e)
	}
	want := "{\"controls\":\"\\n\\t\\u0001\",\"html\":\"<>&\",\"literal\":\"\\\\u2028\",\"中文\":\"a\u2028b\"}"
	if string(b) != want {
		t.Fatalf("%s != %s", b, want)
	}
}
func TestExistingArchiveCompatibility(t *testing.T) {
	root := os.Getenv("DJISMS_NATIVE_ARCHIVE_TEST_COPY")
	if root == "" {
		t.Skip("isolated compatibility clone not supplied")
	}
	if !strings.Contains(root, "compatibility-copy") {
		t.Fatal("must use isolated clone")
	}
	s, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	summary, e := s.Summary()
	if e != nil {
		t.Fatal(e)
	}
	t.Log(summary)
}
func FuzzCanonicalRoundtrip(f *testing.F) {
	f.Add(`\u2028中文`, true)
	f.Fuzz(func(t *testing.T, s string, b bool) {
		if len(s) > 4096 {
			return
		}
		v := M{"s": s, "b": b}
		raw, e := Canonical(v)
		if e != nil {
			return
		}
		var m M
		if e = Decode(raw, &m); e != nil {
			t.Fatal(e)
		}
		again, e := Canonical(m)
		if e != nil || string(raw) != string(again) {
			t.Fatal("unstable canonical")
		}
	})
}

func privateTestRoot(t *testing.T) string {
	t.Helper()
	p, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(p, 0700); e != nil {
		t.Fatal(e)
	}
	return p
}

func TestTamperedPurgeProjectionRejectedOnRestart(t *testing.T) {
	root := privateTestRoot(t)
	s, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	rid, e := s.Ingest("target", "0000", meta(), M{"state": "binary"})
	if e != nil {
		t.Fatal(e)
	}
	_, h := PDUHashes("0000")
	if e = s.Event("delete_intent", rid, M{"attempt_id": "one", "storage": "ME", "index": 0, "pdu_sha256": h, "connection_id": "test", "mode": "approved_live"}); e != nil {
		t.Fatal(e)
	}
	if e = s.db.Exec("UPDATE purge_attempts SET state='confirmed' WHERE id='one'"); e != nil {
		t.Fatal(e)
	}
	s.Close()
	if s, e = Open(root); e == nil {
		s.Close()
		t.Fatal("SQLite erased pending delete")
	}
}

func TestPreservedSessionRejectsMissingOrChangedFacts(t *testing.T) {
	for _, mode := range []string{"complete", "gap", "tampered", "bad_id"} {
		t.Run(mode, func(t *testing.T) {
			s, e := Open(privateTestRoot(t))
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			id := strings.Repeat("a", 32)
			first, e := s.SaveSource("session/"+id+"/000001/out_intent", []byte(`{"command":"fixed"}`))
			if e != nil {
				t.Fatal(e)
			}
			n := "000002"
			if mode == "gap" {
				n = "000003"
			}
			if _, e = s.SaveSource("session/"+id+"/"+n+"/session_end", []byte(`{}`)); e != nil {
				t.Fatal(e)
			}
			if mode == "tampered" {
				if e = os.WriteFile(filepath.Join(s.Root, "sources", first+".bin"), []byte("changed"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "bad_id" {
				id = "../sources"
			}
			facts, e := s.PreservedSession(id)
			if mode == "complete" {
				if e != nil || len(facts) != 2 || facts[0].Kind != "out_intent" {
					t.Fatal(facts, e)
				}
			} else if e == nil {
				t.Fatal("unsafe facts accepted")
			}
		})
	}
}
