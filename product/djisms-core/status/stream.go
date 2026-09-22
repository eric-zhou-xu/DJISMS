package status

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const unsolicitedPolicy = "readonly-status-v1"

type StreamEvent struct {
	Kind      string `json:"kind"`
	Phase     string `json:"phase"`
	Offset    int    `json:"stream_byte_offset"`
	Hex       string `json:"hex"`
	Text      string `json:"text,omitempty"`
	Syntax    string `json:"syntax,omitempty"`
	RequestID uint64 `json:"response_request_id,omitempty"`
}

type atStream struct {
	command, prefix string
	identity        bool
	allowEmpty      bool
	payload         []string
	events          []StreamEvent
	pending         []byte
	pendingCR       bool
	offset, start   int
	requestID       uint64
	state           string // before_write, awaiting_echo, awaiting_ok, complete
	echo, terminal  bool
	fault           error
}

func newATStream(requestID uint64, q Query) *atStream {
	d := definitions[q-1]
	return &atStream{requestID: requestID, state: "before_write", command: d.command, prefix: d.prefix, identity: d.identity, allowEmpty: q == SubscriberNumber}
}
func (p *atStream) event(kind string, start int, raw []byte, text, syntax string, request uint64) {
	p.events = append(p.events, StreamEvent{Kind: kind, Phase: p.state, Offset: start, Hex: hex.EncodeToString(raw), Text: text, Syntax: syntax, RequestID: request})
}
func (p *atStream) fail(reason string) error { p.fault = errors.New(reason); return p.fault }
func (p *atStream) markWritten() error {
	if p.fault != nil || p.state != "before_write" || len(p.pending) != 0 {
		return p.fail("cannot attribute response across an unresolved input boundary")
	}
	p.state = "awaiting_echo"
	return nil
}
func modemError(s string) bool {
	return s == "ERROR" || s == "NO CARRIER" || s == "BUSY" || s == "NO ANSWER" || s == "NO DIALTONE" || s == "NO DIAL TONE" || s == "CONNECT" || strings.HasPrefix(s, "CONNECT ") || strings.HasPrefix(s, "+CME ERROR") || strings.HasPrefix(s, "+CMS ERROR")
}

// This is a bounded line envelope, NOT a semantic decoder or a guarantee that
// an unknown vendor event has no continuation. Never unquote, trim, case-fold,
// split fields, or search substrings to recognize an echo or terminal result.
func textEnvelope(s string) bool {
	return strings.Count(s, `"`)%2 == 0
}

func unsupportedResponse(s string) bool {
	// AT command-shaped input and numeric result mode are not unsolicited text.
	if strings.HasPrefix(strings.ToUpper(s), "AT") {
		return true
	}
	digits := s != ""
	for _, c := range s {
		digits = digits && c >= '0' && c <= '9'
	}
	return digits
}

func multilineIntroducer(s string) bool {
	// Standard SMS bodies may themselves contain AT/OK lines. This AT-only
	// gate has no body parser. These are unsupported protocol forms, not URC
	// content allowlists. Unknown vendor multiline forms remain a limitation.
	name, _, _ := strings.Cut(s, ":")
	switch strings.TrimSpace(name) {
	case "+CMT", "+CDS", "+CBM":
		return true
	}
	return false
}
func (p *atStream) line(raw []byte, contentLen int) error {
	text := string(raw[:contentLen])
	start := p.start
	defer func() { p.pending = nil; p.pendingCR = false }()
	if text == "" {
		p.event("framing", start, raw, "", "", 0)
		return nil
	}
	// Reserved response/error tokens take precedence over generic text events.
	if modemError(text) {
		p.event("modem_error", start, raw, text, "", 0)
		return p.fail("modem error/unsupported terminal result")
	}
	if text == p.command {
		if p.state != "awaiting_echo" {
			p.event("unexpected_response", start, raw, text, "", 0)
			return p.fail("stale or duplicate echo")
		}
		p.event("command_echo", start, raw, text, "", p.requestID)
		p.echo = true
		p.state = "awaiting_ok"
		return nil
	}
	if text == "OK" {
		if p.state != "awaiting_ok" {
			p.event("unexpected_response", start, raw, text, "", 0)
			return p.fail("OK without this request's preceding echo or duplicate OK")
		}
		p.event("terminal_ok", start, raw, text, "", p.requestID)
		p.terminal = true
		p.state = "complete"
		return nil
	}
	if !textEnvelope(text) {
		p.event("invalid_text_envelope", start, raw, text, "", 0)
		return p.fail("unbalanced quoted text; multiline/partial content unsupported")
	}
	if unsupportedResponse(text) || multilineIntroducer(text) {
		p.event("unsupported_protocol", start, raw, text, "", 0)
		return p.fail("unsupported command/result mode or multiline body")
	}
	// Only expected data after this command's exact echo can be its body.
	if p.state == "awaiting_ok" && ((p.prefix != "" && strings.HasPrefix(text, p.prefix)) || (p.identity && !strings.HasPrefix(text, "+") && !strings.HasPrefix(text, "^"))) {
		p.payload = append(p.payload, text)
		p.event("response_data", start, raw, text, "", p.requestID)
		return nil
	}
	p.event("unsolicited_text", start, raw, text, "printable_ascii_crlf", 0)
	return nil
}

// CRLF can span reads. Bare LF/CR are invalid, except the exact awaited AT echo
// followed by CR CRLF (the command CR plus the response's leading CRLF).
// Every received byte belongs to an event or the unfinished-fragment evidence.
func (p *atStream) feed(b []byte) error {
	if p.fault != nil {
		return p.fault
	}
	for i, c := range b {
		if p.pendingCR {
			if c == '\n' {
				p.pending = append(p.pending, c)
				p.offset++
				if err := p.line(p.pending, len(p.pending)-2); err != nil {
					p.retainTail(b[i+1:])
					return err
				}
				continue
			}
			if c != '\r' || p.state != "awaiting_echo" || string(p.pending) != p.command+"\r" {
				p.event("invalid_framing", p.start, p.pending, "", "", 0)
				p.pending = nil
				p.pendingCR = false
				p.retainTail(b[i:])
				return p.fail("CR not followed by LF outside exact AT echo")
			}
			if err := p.line(p.pending, len(p.pending)-1); err != nil {
				p.retainTail(b[i:])
				return err
			}
		}
		if len(p.pending) == 0 {
			p.start = p.offset
		}
		p.pending = append(p.pending, c)
		p.offset++
		if c == '\r' {
			p.pendingCR = true
			continue
		}
		if c == '\n' {
			p.event("invalid_framing", p.start, p.pending, "", "", 0)
			p.pending = nil
			p.retainTail(b[i+1:])
			return p.fail("bare LF in AT stream")
		}
		if c < 0x20 || c > 0x7e {
			p.event("unknown_binary", p.start, p.pending, "", "", 0)
			p.pending = nil
			p.retainTail(b[i+1:])
			return p.fail("binary/control byte in AT stream")
		}
		if len(p.pending) > 512 {
			p.event("unknown_oversize_line", p.start, p.pending, "", "", 0)
			p.pending = nil
			p.retainTail(b[i+1:])
			return p.fail("line exceeds 512 bytes")
		}
	}
	return nil
}
func (p *atStream) retainTail(b []byte) {
	if len(b) > 0 {
		p.event("unparsed_after_fault", p.offset, b, "", "", 0)
		p.offset += len(b)
	}
}
func (p *atStream) finishFragment(reason string) {
	if len(p.pending) > 0 {
		p.event("unknown_fragment", p.start, p.pending, "", "", 0)
		p.pending = nil
		p.pendingCR = false
		p.fail(reason)
	}
}
func (p *atStream) complete() bool {
	return p.fault == nil && p.state == "complete" && len(p.pending) == 0
}
func (p *atStream) boundary() bool { return p.fault == nil && len(p.pending) == 0 }
func (p *atStream) validateSuccess() error {
	if !p.complete() || !p.echo || !p.terminal || (len(p.payload) == 0 && !p.allowEmpty) {
		return fmt.Errorf("response not unambiguously complete")
	}
	return nil
}
