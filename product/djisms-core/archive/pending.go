package archive

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A crash after linking a complete durable file may leave its staging hardlink.
// Recover only when an already-published file is byte-for-byte identical. Keep
// the staging bytes under recovery/; unmatched/partial files remain untouched
// and the runtime refuses device operations while they exist.
func (s *Store) retainPublishedPending() error {
	for _, dir := range []string{"raw", "journal", "sources"} {
		pending, e := filepath.Glob(filepath.Join(s.Root, dir, ".pending-*"))
		if e != nil {
			return e
		}
		for _, path := range pending {
			b, e := Read(path)
			if e != nil {
				return e
			}
			target := ""
			switch dir {
			case "raw":
				var v M
				if Decode(b, &v) == nil && validateEnvelope(v) == nil {
					target = filepath.Join(s.Root, dir, text(v["id"])+".json")
				}
			case "journal":
				var v M
				if Decode(b, &v) == nil && integer(v["seq"]) > 0 {
					target = filepath.Join(s.Root, dir, fmt.Sprintf("%012d.json", integer(v["seq"])))
				}
			case "sources":
				target = filepath.Join(s.Root, dir, Hash(b)+".bin")
			}
			if target == "" {
				continue
			}
			original, e := Read(target)
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return e
			}
			if Hash(original) != Hash(b) {
				continue
			}
			retained := filepath.Join(s.Root, "recovery", dir+"-"+strings.TrimPrefix(filepath.Base(path), "."))
			if _, e = os.Lstat(retained); !os.IsNotExist(e) {
				return fmt.Errorf("pending evidence destination already exists or is unavailable: %w", e)
			}
			if e = os.Rename(path, retained); e != nil {
				return e
			}
			if e = SyncDir(filepath.Dir(path)); e != nil {
				return e
			}
			if e = SyncDir(filepath.Dir(retained)); e != nil {
				return e
			}
			if e = s.event("published_staging_retained", "", M{"published": filepath.Base(target), "directory": dir, "retained": filepath.Base(retained), "sha256": Hash(b)}); e != nil {
				return e
			}
		}
	}
	return nil
}
