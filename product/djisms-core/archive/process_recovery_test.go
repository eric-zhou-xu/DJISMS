package archive

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// The child is the same test binary, uses only an isolated directory and never
// imports a USB transport. SIGKILL bypasses deferred Close and SQLite cleanup.
func TestCrashChild(t *testing.T) {
	root, point := os.Getenv("DJISMS_CRASH_ROOT"), os.Getenv("DJISMS_CRASH_POINT")
	if root == "" {
		return
	}
	s, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	s.Fault = func(p string) error {
		if p == point {
			if e := os.WriteFile(filepath.Join(root, "reached"), []byte(point), 0600); e != nil {
				t.Fatal(e)
			}
			_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
			os.Exit(74)
		}
		return nil
	}
	rid, e := s.Ingest("process-crash", "0000", meta(), M{"state": "binary"})
	if e != nil {
		t.Fatal(e)
	}
	_, hash := PDUHashes("0000")
	if e = s.Event("delete_intent", rid, M{"attempt_id": "fixture", "storage": "ME", "index": 0, "pdu_sha256": hash, "connection_id": "session-test", "mode": "simulation"}); e != nil {
		t.Fatal(e)
	}
	if e = s.Event("delete_result", rid, M{"attempt_id": "fixture", "state": "unknown", "response": "offline interrupted result"}); e != nil {
		t.Fatal(e)
	}
	t.Fatal("crash point not reached")
}
func TestProcessKillRecoveryBoundaries(t *testing.T) {
	for _, point := range []string{"raw_durable", "journal_durable:raw_preserved", "projection_uncommitted:raw_preserved", "projection_committed:raw_preserved", "journal_durable:archive_reconciled", "journal_durable:delete_intent", "projection_uncommitted:delete_intent", "projection_committed:delete_intent", "journal_durable:delete_result"} {
		t.Run(point, func(t *testing.T) {
			root := privateTestRoot(t)
			child := exec.Command(os.Args[0], "-test.run=^TestCrashChild$")
			child.Env = append(os.Environ(), "DJISMS_CRASH_ROOT="+root, "DJISMS_CRASH_POINT="+point)
			output, e := child.CombinedOutput()
			if e == nil {
				t.Fatal("child did not terminate")
			}
			reached, e := os.ReadFile(filepath.Join(root, "reached"))
			if e != nil || string(reached) != point {
				t.Fatalf("point not reached: %s %v", output, e)
			}
			originals := map[string]string{}
			for _, dir := range []string{"raw", "journal"} {
				paths, _ := filepath.Glob(filepath.Join(root, dir, "*.json"))
				for _, p := range paths {
					b, e := Read(p)
					if e != nil {
						t.Fatal(e)
					}
					originals[p] = Hash(b)
				}
			}
			s, e := Open(root)
			if e != nil {
				t.Fatal("restart", e)
			}
			defer s.Close()
			rid := Hash([]byte("process-crash"))
			if _, e = s.Verify(rid); e != nil {
				t.Fatal("raw recovery", e)
			}
			summary, e := s.Summary()
			if e != nil {
				t.Fatal(e)
			}
			want := 0
			if strings.Contains(point, "delete_") {
				want = 1
			}
			if summary["unfinished_delete_attempts"] != want {
				t.Fatal("unknown delete escaped", summary)
			}
			if want == 1 {
				if eligible, e := s.Eligible(rid); e != nil || eligible {
					t.Fatal("unknown result eligible", e)
				}
			}
			for p, hash := range originals {
				b, e := Read(p)
				if e != nil || Hash(b) != hash {
					t.Fatal("durable evidence changed", p, e)
				}
			}
		})
	}
}
func TestFilesystemPermissionFailureRetainsRaw(t *testing.T) {
	for _, dir := range []string{"raw", "journal", "sources"} {
		t.Run(dir, func(t *testing.T) {
			root := privateTestRoot(t)
			s, e := Open(root)
			if e != nil {
				t.Fatal(e)
			}
			baseline, e := s.Ingest("baseline", "0000", meta(), M{"state": "binary"})
			if e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(root, dir)
			if e = os.Chmod(path, 0500); e != nil {
				t.Fatal(e)
			}
			if dir == "sources" {
				_, e = s.SaveSource("failure", []byte("offline evidence"))
			} else {
				_, e = s.Ingest("write-denied", "0001", meta(), M{"state": "binary"})
			}
			if e == nil {
				t.Fatal("permission failure ignored")
			}
			if e = os.Chmod(path, 0700); e != nil {
				t.Fatal(e)
			}
			if e = s.Close(); e != nil {
				t.Fatal(e)
			}
			s, e = Open(root)
			if e != nil {
				t.Fatal("reopen", e)
			}
			defer s.Close()
			if _, e = s.Verify(baseline); e != nil {
				t.Fatal(e)
			}
			if dir == "journal" {
				if _, e = s.Verify(Hash([]byte("write-denied"))); e != nil {
					t.Fatal("orphan raw was lost", e)
				}
			}
		})
	}
}
func TestSQLiteFullStopsProjectionAndReplaysJournal(t *testing.T) {
	root := privateTestRoot(t)
	s, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	rows, e := s.db.Query("PRAGMA page_count")
	if e != nil {
		t.Fatal(e)
	}
	pages := integer(rows[0]["page_count"])
	if e = s.db.Script(fmt.Sprintf("PRAGMA max_page_count=%d", pages)); e != nil {
		t.Fatal(e)
	}
	e = s.Event("offline_large_event", "", M{"payload": strings.Repeat("x", 200000)})
	if e == nil || !strings.Contains(e.Error(), "SQLite 13") {
		t.Fatalf("expected real SQLITE_FULL, got %v", e)
	}
	if !s.poisoned {
		t.Fatal("failed projection still writable")
	}
	if e = s.db.Script("PRAGMA max_page_count=1073741823"); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	event, e := s.LatestEvent("offline_large_event")
	if e != nil || len(text(event["payload"])) != 200000 {
		t.Fatal("journal replay lost full event", e)
	}
}

func TestPublishedStagingRecoveredButPartialEvidenceRetained(t *testing.T) {
	root := privateTestRoot(t)
	s, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	rid, e := s.Ingest("staging", "0000", meta(), M{"state": "binary"})
	if e != nil {
		t.Fatal(e)
	}
	original := filepath.Join(root, "raw", rid+".json")
	duplicate := filepath.Join(root, "raw", ".pending-fixture-published")
	if e = os.Link(original, duplicate); e != nil {
		t.Fatal(e)
	}
	partial := filepath.Join(root, "raw", ".pending-fixture-incomplete")
	if e = os.WriteFile(partial, []byte(`{"schema":`), 0600); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	summary, e := s.Summary()
	if e != nil || summary["pending_temp_files"] != 1 {
		t.Fatal(summary, e)
	}
	a, e := Read(original)
	if e != nil {
		t.Fatal(e)
	}
	b, e := Read(filepath.Join(root, "recovery", "raw-pending-fixture-published"))
	if e != nil || Hash(a) != Hash(b) {
		t.Fatal("staging bytes lost", e)
	}
	b, e = Read(partial)
	if e != nil || string(b) != `{"schema":` {
		t.Fatal("partial evidence changed", e)
	}
}
