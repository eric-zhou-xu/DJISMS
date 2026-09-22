// Package smsarchive decodes previously persisted evidence. It performs no I/O.
package smsarchive

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/iniwex5/vohive/internal/smsreceive"
	"github.com/iniwex5/vohive/pkg/smscodec"
	"github.com/warthog618/sms"
	"github.com/warthog618/sms/encoding/tpdu"
	"sort"
	"time"
	"unicode/utf8"
)

type Archive struct {
	SourceReceiptSHA256 string                  `json:"source_receipt_sha256"`
	Messages            []smsreceive.RawMessage `json:"messages"`
}
type Record struct {
	Index               int    `json:"storage_index"`
	Status              int    `json:"reported_status"`
	RawPDUSHA256        string `json:"raw_pdu_ascii_sha256"`
	Sender              string `json:"sender,omitempty"`
	Timestamp           string `json:"timestamp,omitempty"`
	DCS                 byte   `json:"dcs"`
	Text                string `json:"text,omitempty"`
	BinaryHex           string `json:"binary_hex,omitempty"`
	Error               string `json:"error,omitempty"`
	RefBits             int    `json:"reference_bits,omitempty"`
	Ref                 int    `json:"reference,omitempty"`
	Total               int    `json:"total,omitempty"`
	Seq                 int    `json:"sequence,omitempty"`
	ATTrailingHex       string `json:"after_at_declared_length_hex,omitempty"`
	UDLTrailingHex      string `json:"after_udl_declared_length_hex,omitempty"`
	SpareBitsNormalized bool   `json:"gsm7_spare_bits_normalized"`
	p                   *tpdu.TPDU
	key                 string
}
type Group struct {
	Indices []int  `json:"storage_indices"`
	State   string `json:"state"`
	Text    string `json:"text,omitempty"`
	Error   string `json:"error,omitempty"`
}
type Result struct {
	ArchiveSHA256 string   `json:"raw_archive_sha256"`
	Records       []Record `json:"records"`
	Groups        []Group  `json:"multipart_groups"`
}

func Decode(raw []byte) (Result, error) {
	r := Result{ArchiveSHA256: fmt.Sprintf("%x", sha256.Sum256(raw)), Records: []Record{}, Groups: []Group{}}
	var a Archive
	if e := json.Unmarshal(raw, &a); e != nil {
		return r, e
	}
	if len(a.SourceReceiptSHA256) != 64 {
		return r, errors.New("missing receipt provenance")
	}
	seen := map[int]bool{}
	groups := map[string][]int{}
	for _, m := range a.Messages {
		if seen[m.Index] {
			return r, errors.New("duplicate storage index")
		}
		seen[m.Index] = true
		v := decodeOne(m)
		r.Records = append(r.Records, v)
		if v.key != "" {
			groups[v.key] = append(groups[v.key], len(r.Records)-1)
		}
	}
	keys := []string{}
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ids := groups[k]
		g := Group{State: "incomplete", Indices: []int{}}
		n := r.Records[ids[0]].Total
		parts := make([]*tpdu.TPDU, n)
		conflict := false
		var low, high time.Time
		for _, id := range ids {
			v := r.Records[id]
			g.Indices = append(g.Indices, v.Index)
			if v.Seq < 1 || v.Seq > n || parts[v.Seq-1] != nil {
				conflict = true
				continue
			}
			parts[v.Seq-1] = v.p
			ts := v.p.SCTS.Time
			if low.IsZero() || ts.Before(low) {
				low = ts
			}
			if high.IsZero() || ts.After(high) {
				high = ts
			}
		}
		if high.Sub(low) > 24*time.Hour {
			conflict = true
		}
		if conflict {
			g.State = "ambiguous"
			g.Error = "duplicate sequence or timestamp collision; no merge"
		} else if len(ids) == n {
			text, e := sms.Decode(parts)
			if e != nil {
				g.State = "decode_error"
				g.Error = e.Error()
			} else if !utf8.Valid(text) {
				g.State = "decode_error"
				g.Error = "invalid UTF-8"
			} else {
				g.State = "complete"
				g.Text = string(text)
			}
		}
		r.Groups = append(r.Groups, g)
	}
	return r, nil
}
func decodeOne(m smsreceive.RawMessage) (r Record) {
	r.Index = m.Index
	r.Status = m.Status
	r.RawPDUSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(m.PDU)))
	fail := func(e error) Record { r.Error = e.Error(); return r }
	raw, e := hex.DecodeString(m.PDU)
	if e != nil || len(raw) == 0 {
		return fail(errors.New("invalid raw hex"))
	}
	start := 1 + int(raw[0])
	end := start + m.TPDULength
	if m.TPDULength < 1 || start >= len(raw) || end > len(raw) {
		return fail(errors.New("AT declared TPDU exceeds preserved PDU"))
	}
	r.ATTrailingHex = hex.EncodeToString(raw[end:])
	b := append([]byte(nil), raw[start:end]...)
	// Receiving scope: other stored types remain losslessly archived, not guessed.
	if b[0]&3 != 0 || m.Status > 1 {
		return fail(errors.New("stored non-DELIVER type retained without receive decoding"))
	}
	want, ok := smscodec.DeliverTPDUDeclaredLength(b)
	if !ok {
		return fail(errors.New("invalid or truncated DELIVER layout"))
	}
	r.UDLTrailingHex = hex.EncodeToString(b[want:])
	b = b[:want]
	// Only unused GSM7 bits are normalized in this derivative copy.
	pos := 3 + (int(b[1])+1)/2
	dcs := tpdu.DCS(b[pos+1])
	udl := int(b[pos+9])
	alpha, e := dcs.Alphabet()
	if e != nil {
		return fail(e)
	}
	if alpha == tpdu.Alpha7Bit && udl > 0 && udl*7%8 != 0 {
		mask := byte((1 << uint(udl*7%8)) - 1)
		last := len(b) - 1
		if b[last]&^mask != 0 {
			b[last] &= mask
			r.SpareBitsNormalized = true
		}
	}
	p, e := sms.Unmarshal(b)
	if e != nil {
		return fail(e)
	}
	r.p = p
	r.Sender = p.OA.Number()
	r.Timestamp = p.SCTS.Time.Format(time.RFC3339)
	r.DCS = byte(p.DCS)
	extra := tpdu.UserDataHeader{}
	count := 0
	for _, ie := range p.UDH {
		if ie.ID == 0 || ie.ID == 8 {
			count++
			if (ie.ID == 0 && len(ie.Data) != 3) || (ie.ID == 8 && len(ie.Data) != 4) {
				return fail(errors.New("invalid concatenation IE"))
			}
			r.RefBits = 8
			if ie.ID == 8 {
				r.RefBits = 16
			}
		} else {
			extra = append(extra, ie)
		}
	}
	if count > 1 {
		return fail(errors.New("multiple concatenation IEs"))
	}
	if count == 1 {
		var valid bool
		r.Total, r.Seq, r.Ref, valid = p.UDH.ConcatInfo()
		if !valid || r.Total < 1 || r.Seq < 1 || r.Seq > r.Total {
			return fail(errors.New("invalid concatenation sequence"))
		}
		// Other UDH (ports, language tables), PID, DCS and reference width all constrain identity.
		extraJSON, _ := json.Marshal(extra)
		r.key = fmt.Sprintf("%s|%d|%d|%d|%d|%d|%s", r.Sender, r.RefBits, r.Ref, r.Total, p.DCS, p.PID, extraJSON)
	}
	if alpha == tpdu.Alpha8Bit {
		r.BinaryHex = hex.EncodeToString(p.UD)
		r.key = ""
		return r
	}
	msg, e := sms.Decode([]*tpdu.TPDU{p})
	if e != nil {
		if count == 1 {
			r.Error = "fragment decode deferred: " + e.Error()
			return r
		}
		return fail(e)
	}
	if !utf8.Valid(msg) {
		return fail(errors.New("invalid UTF-8"))
	}
	r.Text = string(msg)
	return r
}
