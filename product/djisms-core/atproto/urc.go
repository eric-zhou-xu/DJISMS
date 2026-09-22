// Package atproto classifies asynchronous subframes independently of AT replies.
// Callers must preserve transport bytes durably BEFORE calling FeedLine.
package atproto

import (
	"encoding/csv"
	"encoding/hex"
	"errors"
	"regexp"
	"strconv"
	"strings"
)

type Frame struct {
	Kind       string `json:"kind"`
	Header     string `json:"raw_header"`
	HeaderHex  string `json:"raw_header_line_hex"`
	PDU        string `json:"raw_pdu"`
	PDULineHex string `json:"raw_pdu_line_hex"`
	Length     int    `json:"reported_tpdu_octets"`
	Offset     int    `json:"stream_offset"`
	End        int    `json:"stream_end"`
}
type Demux struct{ pending *Frame }

func (d *Demux) Pending() bool { return d.pending != nil }
func PDU(s string, length int, kind string) error {
	b, e := hex.DecodeString(s)
	if e != nil || len(b) == 0 || length < 1 || length > 255 {
		return errors.New("invalid asynchronous PDU")
	}
	if kind == "CBM" {
		if len(b) != length {
			return errors.New("CBM length mismatch")
		}
		return nil
	}
	if int(b[0])+1 >= len(b) || len(b)-int(b[0])-1 != length {
		return errors.New("SMSC/TPDU length mismatch")
	}
	typ := b[int(b[0])+1] & 3
	if (kind == "CMT" && typ != 0) || (kind == "CDS" && typ != 2) {
		return errors.New("asynchronous TPDU type mismatch")
	}
	return nil
}
func (d *Demux) FeedLine(s string, raw []byte, offset int) (bool, *Frame, error) {
	if d.pending != nil {
		f := *d.pending
		if e := PDU(s, f.Length, f.Kind); e != nil {
			return true, nil, e
		}
		f.PDU = s
		f.PDULineHex = hex.EncodeToString(raw)
		f.End = offset + len(raw)
		d.pending = nil
		return true, &f, nil
	}
	for _, kind := range []string{"CMT", "CDS", "CBM"} {
		tail, ok := strings.CutPrefix(s, "+"+kind+":")
		if !ok {
			continue
		}
		r := csv.NewReader(strings.NewReader(strings.TrimSpace(tail)))
		r.TrimLeadingSpace = true
		r.FieldsPerRecord = -1
		fields, e := r.Read()
		if e != nil {
			return true, nil, e
		}
		want := 1
		if kind == "CMT" {
			want = 2
		}
		if len(fields) != want {
			return true, nil, errors.New("not a PDU-mode asynchronous header")
		}
		n, e := strconv.Atoi(fields[len(fields)-1])
		if e != nil || n < 1 || n > 255 {
			return true, nil, errors.New("asynchronous length bound")
		}
		if kind == "CMT" && len(fields[0]) > 64 {
			return true, nil, errors.New("alpha exceeds bound")
		}
		d.pending = &Frame{Kind: kind, Header: s, HeaderHex: hex.EncodeToString(raw), Length: n, Offset: offset}
		return true, nil, nil
	}
	return false, nil, nil
}

var single = regexp.MustCompile(`^(RING|NO CARRIER|BUSY|NO ANSWER|NO DIALTONE|NO DIAL TONE|RDY|SMS Ready|Call Ready|PB DONE|\+QIND: (SMS DONE|PB DONE)|\+CTZV: [+-]?[0-9]{1,2}|\+CTZE: "[+-][0-9]{2}",[0-2],"[0-9]{4}/[0-9]{2}/[0-9]{2},[0-9]{2}:[0-9]{2}:[0-9]{2}"|\+CPIN: (READY|SIM PIN|SIM PUK|NOT INSERTED))$`)
var registration = regexp.MustCompile(`^\+(CREG|CGREG|CEREG): [0-9]{1,2}(,"[0-9A-Fa-f]*","[0-9A-Fa-f]*"(,[0-9]{1,2})?)?$`)
var notice = regexp.MustCompile(`^\+(CMTI|CDSI|CBMI): "(ME|SM|BM)",([0-9]{1,3})$`)

// KnownSingle does not classify command-response n,stat registration bodies as
// URCs. Unknown printable lines are not silently discarded by consumers.
func KnownSingle(s string) bool {
	return single.MatchString(s) || registration.MatchString(s) || notice.MatchString(s)
}
