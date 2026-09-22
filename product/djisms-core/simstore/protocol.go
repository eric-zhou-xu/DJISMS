package simstore

import (
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/iniwex5/vohive/internal/smsreceive"
	"github.com/iniwex5/vohive/product/djisms-core/atproto"
	"strconv"
	"strings"
)

type Command struct {
	Kind  uint8 `json:"kind"`
	Index int   `json:"index"`
}

func (c Command) wire() (string, error) {
	if c.Kind == 7 || c.Kind == 9 {
		if c.Index < 0 || c.Index > 65535 {
			return "", errors.New("index bound")
		}
		if c.Kind == 7 {
			return fmt.Sprintf("AT+CMGR=%d", c.Index), nil
		}
		return fmt.Sprintf("AT+CMGD=%d", c.Index), nil
	}
	if c.Index != 0 {
		return "", errors.New("unexpected index")
	}
	switch c.Kind {
	case 1:
		return "AT+CMGF?", nil
	case 2:
		return "AT+CPMS?", nil
	case 3:
		return "AT+CNMI?", nil
	case 4:
		return "AT+QCCID", nil
	case 5:
		return "AT+CMGL=4", nil
	case 6:
		return `AT+CPMS="SM"`, nil
	case 8:
		return `AT+CPMS="ME"`, nil
	case 10:
		return "AT+CPMS=?", nil
	}
	return "", errors.New("outside SM command whitelist")
}

type Notice struct {
	Index       int    `json:"index"`
	Offset      int    `json:"stream_offset"`
	LineHex     string `json:"line_hex"`
	ObservedUTC string `json:"observed_utc"`
}
type Direct struct {
	EventID        int    `json:"event_id"`
	ObservedUTC    string `json:"observed_utc"`
	Offset         int    `json:"stream_offset"`
	Header         string `json:"raw_header"`
	HeaderHex      string `json:"raw_header_line_hex"`
	TPDULength     int    `json:"reported_tpdu_octets"`
	PDU            string `json:"raw_pdu"`
	PDULineHex     string `json:"raw_pdu_line_hex"`
	StorageIndex   *int   `json:"storage_index"`
	ReportedStatus *int   `json:"reported_status"`
}
type Response struct {
	Command  Command                 `json:"command"`
	Echo     bool                    `json:"echo"`
	Done     bool                    `json:"done"`
	Lines    []string                `json:"lines"`
	Messages []smsreceive.RawMessage `json:"messages,omitempty"`
}
type framer struct {
	pending       []byte
	offset, start int
	active        *Response
	pendingStored *smsreceive.RawMessage
	demux         atproto.Demux
	asynchronous  []atproto.Frame
	directs       []Direct
	directID      int
	notices       []Notice
	when          string
	fault         error
}

func fields(s string) ([]string, error) {
	r := csv.NewReader(strings.NewReader(strings.TrimSpace(s)))
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = -1
	return r.Read()
}
func (p *framer) boundary() bool {
	return p.fault == nil && len(p.pending) == 0 && !p.demux.Pending() && p.pendingStored == nil
}
func (p *framer) begin(c Command) error {
	if !p.boundary() || p.active != nil {
		return errors.New("not at classified boundary")
	}
	if _, e := c.wire(); e != nil {
		return e
	}
	p.active = &Response{Command: c, Lines: []string{}, Messages: []smsreceive.RawMessage{}}
	return nil
}
func (p *framer) feed(b []byte) error {
	if p.fault != nil {
		return p.fault
	}
	for _, ch := range b {
		if len(p.pending) == 0 {
			p.start = p.offset
		}
		p.offset++
		if len(p.pending) > 0 && p.pending[len(p.pending)-1] == '\r' && ch != '\n' {
			echo := ""
			if p.active != nil {
				echo, _ = p.active.Command.wire()
			}
			if ch == '\r' && p.active != nil && !p.active.Echo && string(p.pending) == echo+"\r" {
				if e := p.line(p.pending, len(p.pending)-1); e != nil {
					p.fault = e
					return e
				}
				p.pending = nil
				p.start = p.offset - 1
			} else {
				p.fault = errors.New("bare CR")
				return p.fault
			}
		}
		p.pending = append(p.pending, ch)
		if ch == '\n' {
			if len(p.pending) < 2 || p.pending[len(p.pending)-2] != '\r' {
				p.fault = errors.New("bare LF")
				return p.fault
			}
			if e := p.line(p.pending, len(p.pending)-2); e != nil {
				p.fault = e
				return e
			}
			p.pending = nil
		} else if (ch < 32 && ch != '\r') || ch > 126 || len(p.pending) > 2048 {
			p.fault = errors.New("binary/control/oversize line")
			return p.fault
		}
	}
	return nil
}
func isHex(s string) bool {
	_, e := hex.DecodeString(s)
	return e == nil && len(s) > 0 && len(s)%2 == 0
}
func (p *framer) line(raw []byte, n int) error {
	s := string(raw[:n])
	if s == "" {
		return nil
	}
	if consumed, frame, e := p.demux.FeedLine(s, raw, p.start); consumed {
		if e != nil {
			return e
		}
		if frame != nil {
			if frame.Kind == "CMT" {
				if p.directID >= 128 {
					return errors.New("direct event bound")
				}
				p.directID++
				p.directs = append(p.directs, Direct{EventID: p.directID, ObservedUTC: p.when, Offset: frame.Offset, Header: frame.Header, HeaderHex: frame.HeaderHex, TPDULength: frame.Length, PDU: frame.PDU, PDULineHex: frame.PDULineHex})
			} else {
				if len(p.asynchronous) >= 128 {
					return errors.New("asynchronous queue bound")
				}
				p.asynchronous = append(p.asynchronous, *frame)
			}
		}
		return nil
	}
	if strings.HasPrefix(s, "+CMTI:") {
		v, e := fields(strings.TrimPrefix(s, "+CMTI:"))
		if e != nil || len(v) != 2 || (v[0] != "ME" && v[0] != "SM") {
			return errors.New("CMTI storage/shape")
		}
		idx, e := strconv.Atoi(v[1])
		if e != nil || idx < 0 || idx > 65535 {
			return errors.New("CMTI index outside ME capacity")
		}
		if len(p.notices) >= 64 {
			return errors.New("notification queue limit")
		}
		p.notices = append(p.notices, Notice{idx, p.start, hex.EncodeToString(raw), p.when})
		return nil
	}
	if p.pendingStored != nil {
		if !isHex(s) {
			return errors.New("stored PDU body must be hex")
		}
		m := p.pendingStored
		m.PDU = s
		m.PDULineHex = hex.EncodeToString(raw)
		p.active.Messages = append(p.active.Messages, *m)
		p.pendingStored = nil
		return nil
	}
	a := p.active
	if s == "ERROR" || strings.HasPrefix(s, "+CMS ERROR:") || strings.HasPrefix(s, "+CME ERROR:") {
		return errors.New("modem terminal error")
	}
	if strings.HasPrefix(s, "AT") {
		if a == nil || a.Echo || a.Done {
			return errors.New("unexpected echo")
		}
		wire, _ := a.Command.wire()
		if s != wire {
			return errors.New("wrong echo")
		}
		a.Echo = true
		return nil
	}
	if s == "OK" {
		if a == nil || !a.Echo || a.Done {
			return errors.New("unexpected OK")
		}
		a.Done = true
		return nil
	}
	if strings.HasPrefix(s, "+CMGR:") || strings.HasPrefix(s, "+CMGL:") {
		list := strings.HasPrefix(s, "+CMGL:")
		if a == nil || !a.Echo || a.Done || (list && a.Command.Kind != 5) || (!list && a.Command.Kind != 7) {
			return errors.New("unexpected stored header")
		}
		if !list && len(a.Messages) != 0 {
			return errors.New("duplicate CMGR header")
		}
		prefix := "+CMGR:"
		if list {
			prefix = "+CMGL:"
		}
		v, e := fields(strings.TrimPrefix(s, prefix))
		if e != nil {
			return e
		}
		if list {
			if len(v) != 3 && len(v) != 4 {
				return errors.New("invalid CMGL fields")
			}
		} else if len(v) != 2 && len(v) != 3 {
			return errors.New("invalid CMGR fields")
		}
		idx := a.Command.Index
		if list {
			idx, e = strconv.Atoi(v[0])
			if e != nil || idx < 0 || idx > 65535 {
				return errors.New("CMGL index bound")
			}
			v = v[1:]
			for _, m := range a.Messages {
				if m.Index == idx {
					return errors.New("duplicate storage index")
				}
			}
		}
		status, e := strconv.Atoi(v[0])
		if e != nil || status < 0 || status > 3 {
			return errors.New("invalid stored status")
		}
		length, e := strconv.Atoi(v[len(v)-1])
		if e != nil || length < 1 || length > 255 {
			return errors.New("invalid stored TPDU length")
		}
		alpha := ""
		if len(v) == 3 {
			alpha = v[1]
		}
		p.pendingStored = &smsreceive.RawMessage{Index: idx, Status: status, Alpha: alpha, TPDULength: length, Header: s, HeaderHex: hex.EncodeToString(raw), Offset: p.start}
		return nil
	}
	for _, prefix := range []string{"+CDS:", "+CBM:", "CONNECT", ">"} {
		if strings.HasPrefix(s, prefix) {
			return errors.New("unsupported protocol mode")
		}
	}
	for _, prefix := range []string{"+CMGF:", "+CPMS:", "+CNMI:", "+QCCID:"} {
		if strings.HasPrefix(s, prefix) {
			expected := map[uint8]string{1: "+CMGF:", 2: "+CPMS:", 3: "+CNMI:", 4: "+QCCID:", 6: "+CPMS:", 8: "+CPMS:", 10: "+CPMS:"}
			if a == nil || expected[a.Command.Kind] != prefix {
				return errors.New("wrong settings response prefix")
			}
			if a == nil || !a.Echo || a.Done {
				return errors.New("settings outside response")
			}
			a.Lines = append(a.Lines, s)
			return nil
		}
	}
	if isHex(s) {
		return errors.New("orphan PDU")
	}
	if atproto.KnownSingle(s) {
		return nil
	}
	return errors.New("unclassified asynchronous line")
}
func (p *framer) finish() (Response, error) {
	if p.active == nil || !p.active.Done || !p.active.Echo || !p.boundary() {
		return Response{}, errors.New("incomplete response")
	}
	a := *p.active
	if a.Command.Kind == 7 {
		if len(a.Messages) != 1 || len(a.Lines) != 0 {
			return a, errors.New("missing CMGR PDU")
		}
	} else if a.Command.Kind == 5 {
		if len(a.Lines) != 0 {
			return a, errors.New("unexpected settings in CMGL")
		}
	} else if a.Command.Kind == 9 {
		if len(a.Lines) != 0 || len(a.Messages) != 0 {
			return a, errors.New("unexpected delete response data")
		}
	} else if len(a.Lines) != 1 {
		return a, errors.New("missing/duplicate settings")
	}
	p.active = nil
	return a, nil
}
