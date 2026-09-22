package archive

/*
#include <sys/mount.h>
#include <stdlib.h>
#include <string.h>
static int local_fs(const char *path){struct statfs s;if(statfs(path,&s))return 0;return (s.f_flags&MNT_LOCAL)&&(!strcmp(s.f_fstypename,"apfs")||!strcmp(s.f_fstypename,"hfs"));}
*/
import "C"
import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unsafe"
)

func Hash(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
func Canonical(v any) ([]byte, error) {
	// Normalize structs through JSON, then emit Python-compatible ensure_ascii=false
	// canonical bytes without changing literal backslash-u text inside strings.
	var normalized any
	var source bytes.Buffer
	enc := json.NewEncoder(&source)
	enc.SetEscapeHTML(false)
	if e := enc.Encode(v); e != nil {
		return nil, e
	}
	if e := Decode(source.Bytes(), &normalized); e != nil {
		return nil, e
	}
	var out bytes.Buffer
	var put func(any) error
	quote := func(s string) {
		out.WriteByte('"')
		for _, r := range s {
			switch r {
			case '"':
				out.WriteString(`\"`)
			case '\\':
				out.WriteString(`\\`)
			case '\b':
				out.WriteString(`\b`)
			case '\f':
				out.WriteString(`\f`)
			case '\n':
				out.WriteString(`\n`)
			case '\r':
				out.WriteString(`\r`)
			case '\t':
				out.WriteString(`\t`)
			default:
				if r < 32 {
					fmt.Fprintf(&out, `\u%04x`, r)
				} else {
					out.WriteRune(r)
				}
			}
		}
		out.WriteByte('"')
	}
	put = func(x any) error {
		switch t := x.(type) {
		case nil:
			out.WriteString("null")
		case bool:
			if t {
				out.WriteString("true")
			} else {
				out.WriteString("false")
			}
		case json.Number:
			out.WriteString(t.String())
		case string:
			quote(t)
		case []any:
			out.WriteByte('[')
			for i, v := range t {
				if i > 0 {
					out.WriteByte(',')
				}
				if e := put(v); e != nil {
					return e
				}
			}
			out.WriteByte(']')
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			out.WriteByte('{')
			for i, k := range keys {
				if i > 0 {
					out.WriteByte(',')
				}
				quote(k)
				out.WriteByte(':')
				if e := put(t[k]); e != nil {
					return e
				}
			}
			out.WriteByte('}')
		default:
			return errors.New("unsupported canonical value")
		}
		return nil
	}
	if e := put(normalized); e != nil {
		return nil, e
	}
	return out.Bytes(), nil
}
func Decode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if e := d.Decode(v); e != nil {
		return e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func FullSync(f *os.File) error {
	if e := f.Sync(); e != nil {
		return e
	}
	_, _, e := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), 51, 0)
	if e != 0 {
		return e
	}
	return nil
}
func SyncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	return errors.Join(f.Sync(), f.Close())
}
func ensurePrivate(path string, dir bool) error {
	st, e := os.Lstat(path)
	if e != nil {
		return e
	}
	s, ok := st.Sys().(*syscall.Stat_t)
	if !ok || s.Uid != uint32(os.Getuid()) || st.Mode().Perm()&0077 != 0 || st.Mode()&os.ModeSymlink != 0 || st.IsDir() != dir || (!dir && !st.Mode().IsRegular()) {
		return fmt.Errorf("unsafe archive ownership/type/permissions: expected_dir=%v mode=%v uid_match=%v", dir, st.Mode(), ok && s.Uid == uint32(os.Getuid()))
	}
	return nil
}
func PrivateDir(path string) error {
	path, e := filepath.Abs(path)
	if e != nil {
		return e
	}
	for p := path; ; p = filepath.Dir(p) {
		st, e := os.Lstat(p)
		if e == nil && st.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlink in archive path")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	for _, p := range strings.Split(path, string(filepath.Separator)) {
		if p == "Mobile Documents" || p == "CloudStorage" {
			return errors.New("cloud archive forbidden")
		}
	}
	if e = os.MkdirAll(path, 0700); e != nil {
		return e
	}
	if e = ensurePrivate(path, true); e != nil {
		return e
	}
	p := C.CString(path)
	defer C.free(unsafe.Pointer(p))
	if C.local_fs(p) != 1 {
		return errors.New("local APFS/HFS archive required")
	}
	return SyncDir(filepath.Dir(path))
}
func Read(path string) ([]byte, error) {
	if e := ensurePrivate(path, false); e != nil {
		return nil, e
	}
	fd, e := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	return io.ReadAll(f)
}
func WriteNew(path string, b []byte) error {
	nonce := make([]byte, 16)
	if _, e := rand.Read(nonce); e != nil {
		return e
	}
	tmp := filepath.Join(filepath.Dir(path), ".pending-"+hex.EncodeToString(nonce))
	f, e := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	if e == nil {
		e = FullSync(f)
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return e
	}
	if e = os.Link(tmp, path); e != nil {
		return e
	}
	if e = SyncDir(filepath.Dir(path)); e != nil {
		return e
	}
	if e = os.Remove(tmp); e != nil {
		return e
	}
	return SyncDir(filepath.Dir(path))
}
func PDUHashes(pdu string) (string, string) {
	a := Hash([]byte(pdu))
	b, e := hex.DecodeString(pdu)
	if e != nil || len(b) == 0 {
		return a, ""
	}
	return a, Hash(b)
}
