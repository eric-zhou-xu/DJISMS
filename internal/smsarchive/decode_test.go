package smsarchive

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/iniwex5/vohive/internal/smsreceive"
	"github.com/warthog618/sms/encoding/tpdu"
	"github.com/warthog618/sms/encoding/ucs2"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, index int, text string, seq, total, bits int) smsreceive.RawMessage {
	t.Helper()
	p, e := tpdu.NewDeliver()
	if e != nil {
		t.Fatal(e)
	}
	p.OA = tpdu.NewAddress(tpdu.FromNumber("+8613800138000"))
	p.SCTS = tpdu.Timestamp{Time: time.Date(2026, 9, 18, 3, 0, 0, 0, time.UTC)}
	p.DCS = tpdu.DCS(8)
	p.UD = ucs2.Encode([]rune(text))
	if total > 0 {
		p.FirstOctet |= 0x40
		if bits == 16 {
			p.UDH = tpdu.UserDataHeader{{ID: 8, Data: []byte{0, 7, byte(total), byte(seq)}}}
		} else {
			p.UDH = tpdu.UserDataHeader{{ID: 0, Data: []byte{7, byte(total), byte(seq)}}}
		}
	}
	b, e := p.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	return smsreceive.RawMessage{Index: index, Status: 0, TPDULength: len(b), PDU: "00" + hex.EncodeToString(b)}
}
func runFixture(t *testing.T, ms ...smsreceive.RawMessage) Result {
	t.Helper()
	b, _ := json.Marshal(Archive{SourceReceiptSHA256: strings.Repeat("a", 64), Messages: ms})
	original := append([]byte(nil), b...)
	r, e := Decode(b)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(b, original) || r.ArchiveSHA256 != fmt.Sprintf("%x", sha256.Sum256(original)) {
		t.Fatal("raw changed")
	}
	return r
}
func TestChineseOTPAndRawPadding(t *testing.T) {
	m := fixture(t, 1, "【测试】验证码 012345，五分钟内有效。", 0, 0, 0)
	m.PDU += "ffff"
	r := runFixture(t, m)
	v := r.Records[0]
	if v.Error != "" || v.Text != "【测试】验证码 012345，五分钟内有效。" || v.ATTrailingHex != "ffff" {
		t.Fatal(v)
	}
}
func TestGSM7SpareBits(t *testing.T) {
	b := "04038101F100006250724190410A3754747A0E4ABBCD6F793B4C4FBFDDA0F41CE47ED341617B38CD0E8BD96590F92D07E5DF7539283C1EBFEB6E3A889E87971B"
	r := runFixture(t, smsreceive.RawMessage{Index: 1, TPDULength: len(b) / 2, PDU: "00" + b})
	v := r.Records[0]
	if v.Error != "" || v.Text != "This information is not available for your account type" || !v.SpareBitsNormalized {
		t.Fatal(v)
	}
}
func TestMultipartOrderMissingDuplicatesAndWidth(t *testing.T) {
	a := fixture(t, 1, "第一段", 1, 2, 8)
	b := fixture(t, 2, "第二段", 2, 2, 8)
	r := runFixture(t, b, a)
	if len(r.Groups) != 1 || r.Groups[0].State != "complete" || r.Groups[0].Text != "第一段第二段" {
		t.Fatal(r)
	}
	r = runFixture(t, a)
	if r.Groups[0].State != "incomplete" {
		t.Fatal(r)
	}
	dup := fixture(t, 3, "冲突", 1, 2, 8)
	r = runFixture(t, a, b, dup)
	if r.Groups[0].State != "ambiguous" || r.Groups[0].Text != "" {
		t.Fatal(r)
	}
	other := fixture(t, 4, "第二段", 2, 2, 16)
	r = runFixture(t, a, other)
	if len(r.Groups) != 2 {
		t.Fatal(r)
	}
	for _, g := range r.Groups {
		if g.State != "incomplete" {
			t.Fatal(g)
		}
	}
}
func TestUDHPortsDoNotCrossJoin(t *testing.T) {
	a := fixture(t, 1, "一", 1, 2, 8)
	b := fixture(t, 2, "二", 2, 2, 8)
	raw, _ := hex.DecodeString(b.PDU)
	p := tpdu.TPDU{}
	if e := p.UnmarshalBinary(raw[1:]); e != nil {
		t.Fatal(e)
	}
	p.UDH = append(p.UDH, tpdu.InformationElement{ID: 5, Data: []byte{0, 1, 0, 2}})
	bb, e := p.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	b.PDU = "00" + hex.EncodeToString(bb)
	b.TPDULength = len(bb)
	r := runFixture(t, a, b)
	if len(r.Groups) != 2 {
		t.Fatal(r)
	}
}
func TestSurrogateAcrossFragments(t *testing.T) {
	ms := []smsreceive.RawMessage{fixture(t, 1, "", 1, 2, 8), fixture(t, 2, "", 2, 2, 8)}
	for i := range ms {
		raw, _ := hex.DecodeString(ms[i].PDU)
		p := tpdu.TPDU{}
		if e := p.UnmarshalBinary(raw[1:]); e != nil {
			t.Fatal(e)
		}
		if i == 0 {
			p.UD = []byte{0xd8, 0x3d}
		} else {
			p.UD = []byte{0xde, 0x00}
		}
		b, e := p.MarshalBinary()
		if e != nil {
			t.Fatal(e)
		}
		ms[i].PDU = "00" + hex.EncodeToString(b)
		ms[i].TPDULength = len(b)
	}
	r := runFixture(t, ms...)
	if r.Groups[0].State != "complete" || r.Groups[0].Text != "😀" {
		t.Fatal(r)
	}
}
func TestInvalidPDUAndIndex(t *testing.T) {
	for _, s := range []string{"", "GG", "00", "ff00", "0000"} {
		r := runFixture(t, smsreceive.RawMessage{Index: 1, TPDULength: 100, PDU: s})
		if r.Records[0].Error == "" {
			t.Fatal(s)
		}
	}
	a := fixture(t, 1, "a", 0, 0, 0)
	b, _ := json.Marshal(Archive{SourceReceiptSHA256: strings.Repeat("a", 64), Messages: []smsreceive.RawMessage{a, a}})
	if _, e := Decode(b); e == nil {
		t.Fatal("duplicate index accepted")
	}
}
func FuzzArchiveDecoder(f *testing.F) {
	f.Add([]byte{0}, uint8(1))
	f.Fuzz(func(t *testing.T, b []byte, n uint8) {
		if len(b) > 1024 {
			return
		}
		m := smsreceive.RawMessage{Index: 1, TPDULength: int(n), PDU: hex.EncodeToString(b)}
		r := runFixture(t, m)
		if len(r.Records) != 1 {
			t.Fatal(r)
		}
	})
}
