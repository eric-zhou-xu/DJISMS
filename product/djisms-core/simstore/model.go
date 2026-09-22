package simstore

import (
	"errors"
	"github.com/iniwex5/vohive/internal/smsreceive"
	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Storage struct {
	Name  string `json:"name"`
	Used  int    `json:"used"`
	Total int    `json:"total"`
}

func parseStorage(r Response) ([3]Storage, error) {
	var out [3]Storage
	if len(r.Lines) != 1 || !strings.HasPrefix(r.Lines[0], "+CPMS:") {
		return out, errors.New("missing CPMS")
	}
	v, e := fields(strings.TrimPrefix(r.Lines[0], "+CPMS:"))
	if e != nil || len(v) != 9 {
		return out, errors.New("CPMS shape")
	}
	for i := 0; i < 3; i++ {
		u, e := strconv.Atoi(v[i*3+1])
		t, x := strconv.Atoi(v[i*3+2])
		if e != nil || x != nil || u < 0 || t < 0 || u > t || t > 4096 {
			return out, errors.New("CPMS capacity bound")
		}
		out[i] = Storage{v[i*3], u, t}
	}
	return out, nil
}
func inventory(r Response, used, total int) (map[int]smsreceive.RawMessage, error) {
	m := map[int]smsreceive.RawMessage{}
	for _, v := range r.Messages {
		if v.Index < 0 || v.Index > 65535 || len(m) >= total {
			return nil, errors.New("inventory capacity/index")
		}
		if _, ok := m[v.Index]; ok {
			return nil, errors.New("duplicate index")
		}
		raw, e := hexPDU(v)
		if e != nil || len(raw) == 0 {
			return nil, errors.New("invalid PDU envelope")
		}
		m[v.Index] = v
	}
	if len(m) != used {
		return nil, errors.New("CPMS/inventory differs")
	}
	return m, nil
}
func hexPDU(v smsreceive.RawMessage) ([]byte, error) {
	p, e := decodeHex(v.PDU)
	if e != nil || len(p) == 0 || int(p[0])+1 > len(p) || len(p)-1-int(p[0]) != v.TPDULength {
		return nil, errors.New("PDU length mismatch")
	}
	return p, nil
}
func fingerprint(m map[int]smsreceive.RawMessage) string {
	keys := []int{}
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	a := []archive.M{}
	for _, k := range keys {
		v := m[k]
		a = append(a, archive.M{"index": k, "pdu": v.PDU, "status": v.Status})
	}
	b, _ := archive.Canonical(a)
	return archive.Hash(b)
}
func proveDeletion(before, after map[int]smsreceive.RawMessage, index int) error {
	if _, ok := before[index]; !ok {
		return errors.New("target absent before delete")
	}
	if _, ok := after[index]; ok || len(after) != len(before)-1 {
		return errors.New("target absence/count not proven")
	}
	for k, v := range before {
		if k == index {
			continue
		}
		w, ok := after[k]
		if !ok || v.PDU != w.PDU || v.Status != w.Status || v.TPDULength != w.TPDULength {
			return errors.New("non-target record changed")
		}
	}
	return nil
}

func supportsSM(s string) bool {
	pattern := regexp.MustCompile(`^\+CPMS: *\(("[A-Z]{2}"(?:,"[A-Z]{2}")*)\),\(("[A-Z]{2}"(?:,"[A-Z]{2}")*)\),\(("[A-Z]{2}"(?:,"[A-Z]{2}")*)\)$`)
	m := pattern.FindStringSubmatch(s)
	if len(m) != 4 {
		return false
	}
	for _, name := range strings.Split(m[1], ",") {
		if name == `"SM"` {
			return true
		}
	}
	return false
}
