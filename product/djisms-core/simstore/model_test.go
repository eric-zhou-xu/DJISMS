package simstore

import (
	"github.com/iniwex5/vohive/internal/smsreceive"
	"testing"
)

func TestStorageSeparation(t *testing.T) {
	v, e := parseStorage(Response{Lines: []string{`+CPMS: "SM",6,50,"ME",0,23,"ME",0,23`}})
	if e != nil || v[0].Name != "SM" || v[0].Total != 50 || v[1].Total != 23 {
		t.Fatal(v, e)
	}
	for _, raw := range []string{`+CPMS: "SM",51,50,"ME",0,23,"ME",0,23`, `+CPMS: "SM",0,99999,"ME",0,23,"ME",0,23`} {
		if _, e := parseStorage(Response{Lines: []string{raw}}); e == nil {
			t.Fatal("bad capacity allowed")
		}
	}
}
func TestSingleDeleteProof(t *testing.T) {
	a := smsreceive.RawMessage{Index: 1, Status: 0, PDU: "0000", TPDULength: 1}
	b := smsreceive.RawMessage{Index: 2, Status: 1, PDU: "0001", TPDULength: 1}
	before := map[int]smsreceive.RawMessage{1: a, 2: b}
	if e := proveDeletion(before, map[int]smsreceive.RawMessage{2: b}, 1); e != nil {
		t.Fatal(e)
	}
	for _, after := range []map[int]smsreceive.RawMessage{{}, {1: a, 2: b}, {2: a}, {3: b}} {
		if proveDeletion(before, after, 1) == nil {
			t.Fatal("unsafe result accepted")
		}
	}
}
func TestSMFramingSplits(t *testing.T) {
	wire := "AT+CMGL=4\r\r\n+CMGL: 50,0,,1\r\n0000\r\nOK\r\n"
	for i := 0; i <= len(wire); i++ {
		p := framer{}
		p.begin(Command{Kind: 5})
		if e := p.feed([]byte(wire[:i])); e != nil {
			t.Fatal(i, e)
		}
		if e := p.feed([]byte(wire[i:])); e != nil {
			t.Fatal(i, e)
		}
		r, e := p.finish()
		if e != nil || len(r.Messages) != 1 || r.Messages[0].Index != 50 {
			t.Fatal(i, r, e)
		}
	}
}
func TestNoBulkOrWriteCommand(t *testing.T) {
	for k := uint8(0); k < 20; k++ {
		for _, idx := range []int{-1, 0, 1, 65535, 65536} {
			c := Command{k, idx}
			w, e := c.wire()
			if e == nil && k == 9 && w != "AT+CMGD=0" && w != "AT+CMGD=1" && w != "AT+CMGD=65535" {
				t.Fatal(w)
			}
			if k > 10 && e == nil {
				t.Fatal("unbounded command")
			}
		}
	}
}
func TestOrphanAndUnknownFailClosed(t *testing.T) {
	for _, line := range []string{"OK\r\n", "0000\r\n", "UNKNOWN\r\n", "+CMT: ,1\r\nOK\r\n"} {
		p := framer{}
		if p.feed([]byte(line)) == nil {
			t.Fatal(line)
		}
	}
}

func TestInterleavedSMSAtEverySplit(t *testing.T) {
	wire := "AT+CMGL=4\r\r\n+CMGL: 50,0,,1\r\n+CMT: ,1\r\n0000\r\n+CMTI: \"SM\",49\r\n0000\r\nOK\r\n"
	for i := 0; i <= len(wire); i++ {
		p := framer{}
		p.begin(Command{Kind: 5})
		if e := p.feed([]byte(wire[:i])); e != nil {
			t.Fatal(i, e)
		}
		if e := p.feed([]byte(wire[i:])); e != nil {
			t.Fatal(i, e)
		}
		r, e := p.finish()
		if e != nil || len(r.Messages) != 1 || len(p.directs) != 1 || len(p.notices) != 1 || r.Messages[0].Index != 50 {
			t.Fatal(i, e)
		}
	}
}
