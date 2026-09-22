// Package database wraps the macOS system SQLite library; no downloaded driver.
package database

/*
#cgo LDFLAGS: -lsqlite3
#include <sqlite3.h>
#include <stdlib.h>
static int bind_text(sqlite3_stmt *s,int i,const char *v,int n){return sqlite3_bind_text(s,i,v,n,SQLITE_TRANSIENT);}
*/
import "C"
import (
	"errors"
	"fmt"
	"unsafe"
)

type DB struct{ p *C.sqlite3 }
type Row map[string]any

func Open(path string) (*DB, error) {
	p := C.CString(path)
	defer C.free(unsafe.Pointer(p))
	d := &DB{}
	if rc := C.sqlite3_open_v2(p, &d.p, C.SQLITE_OPEN_READWRITE|C.SQLITE_OPEN_CREATE|C.SQLITE_OPEN_FULLMUTEX, nil); rc != C.SQLITE_OK {
		e := d.err(rc)
		d.Close()
		return nil, e
	}
	return d, nil
}
func (d *DB) err(rc C.int) error {
	return fmt.Errorf("SQLite %d: %s", int(rc), C.GoString(C.sqlite3_errmsg(d.p)))
}
func (d *DB) Close() error {
	if d == nil || d.p == nil {
		return nil
	}
	rc := C.sqlite3_close_v2(d.p)
	if rc != C.SQLITE_OK {
		return d.err(rc)
	}
	d.p = nil
	return nil
}
func (d *DB) Script(q string) error {
	s := C.CString(q)
	defer C.free(unsafe.Pointer(s))
	rc := C.sqlite3_exec(d.p, s, nil, nil, nil)
	if rc != C.SQLITE_OK {
		return d.err(rc)
	}
	return nil
}
func (d *DB) prepare(q string, args []any) (*C.sqlite3_stmt, error) {
	if d == nil || d.p == nil {
		return nil, errors.New("closed database")
	}
	s := C.CString(q)
	defer C.free(unsafe.Pointer(s))
	var st *C.sqlite3_stmt
	if rc := C.sqlite3_prepare_v2(d.p, s, -1, &st, nil); rc != C.SQLITE_OK {
		return nil, d.err(rc)
	}
	if int(C.sqlite3_bind_parameter_count(st)) != len(args) {
		C.sqlite3_finalize(st)
		return nil, errors.New("SQLite parameter count")
	}
	for i, v := range args {
		var rc C.int
		switch x := v.(type) {
		case nil:
			rc = C.sqlite3_bind_null(st, C.int(i+1))
		case string:
			p := C.CString(x)
			rc = C.bind_text(st, C.int(i+1), p, C.int(len(x)))
			C.free(unsafe.Pointer(p))
		case int:
			rc = C.sqlite3_bind_int64(st, C.int(i+1), C.sqlite3_int64(x))
		case int64:
			rc = C.sqlite3_bind_int64(st, C.int(i+1), C.sqlite3_int64(x))
		case bool:
			n := 0
			if x {
				n = 1
			}
			rc = C.sqlite3_bind_int64(st, C.int(i+1), C.sqlite3_int64(n))
		default:
			C.sqlite3_finalize(st)
			return nil, fmt.Errorf("unsupported SQLite parameter %T", v)
		}
		if rc != C.SQLITE_OK {
			e := d.err(rc)
			C.sqlite3_finalize(st)
			return nil, e
		}
	}
	return st, nil
}
func (d *DB) Exec(q string, args ...any) error {
	st, e := d.prepare(q, args)
	if e != nil {
		return e
	}
	defer C.sqlite3_finalize(st)
	rc := C.sqlite3_step(st)
	if rc != C.SQLITE_DONE {
		return d.err(rc)
	}
	return nil
}
func (d *DB) Query(q string, args ...any) ([]Row, error) {
	st, e := d.prepare(q, args)
	if e != nil {
		return nil, e
	}
	defer C.sqlite3_finalize(st)
	rows := []Row{}
	for {
		rc := C.sqlite3_step(st)
		if rc == C.SQLITE_DONE {
			return rows, nil
		}
		if rc != C.SQLITE_ROW {
			return nil, d.err(rc)
		}
		r := Row{}
		for i := C.int(0); i < C.sqlite3_column_count(st); i++ {
			k := C.GoString(C.sqlite3_column_name(st, i))
			switch C.sqlite3_column_type(st, i) {
			case C.SQLITE_NULL:
				r[k] = nil
			case C.SQLITE_INTEGER:
				r[k] = int64(C.sqlite3_column_int64(st, i))
			case C.SQLITE_TEXT:
				r[k] = C.GoStringN((*C.char)(unsafe.Pointer(C.sqlite3_column_text(st, i))), C.sqlite3_column_bytes(st, i))
			default:
				return nil, errors.New("unsupported SQLite result type")
			}
		}
		rows = append(rows, r)
	}
}
