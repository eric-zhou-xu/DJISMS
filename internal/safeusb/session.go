package safeusb

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
)

var ErrLocked = errors.New("Gate 1 locked: no approved interface profile")

// The reviewed list is intentionally empty. Descriptor JSON cannot populate it.
// An entry must be introduced by a separately reviewed source change after approval.
func NewGate1Session(t Transport) *Session { return &Session{transport: t} }

type profile struct {
	layout         Device
	iface, in, out uint8
}

// Transport is the original offline policy interface. Its approved profile list
// remains empty. The separately tagged internal/gate1 native library is not
// wired into this session or either application command.
type Transport interface {
	Claim(context.Context, uint8, uint8, uint8) error
	Query(context.Context, string) (string, error)
	Release(uint8) error
	Close() error
}
type Session struct {
	mu        sync.Mutex
	transport Transport
	approved  []profile
	claimed   bool
	closed    bool
	iface     uint8
}

func (s *Session) Connect(ctx context.Context, d Device) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.claimed {
		return errors.New("session closed or already connected")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := Validate(d); err != nil {
		return err
	}
	for _, p := range s.approved {
		if !matches(p, d) {
			continue
		}
		if s.transport == nil {
			return ErrLocked
		}
		// Exactly one reviewed interface. BUSY/errors never cause retries or fallback.
		if err := s.transport.Claim(ctx, p.iface, p.in, p.out); err != nil {
			return s.failLocked(err)
		}
		s.claimed = true
		s.iface = p.iface
		return nil // no AT probe, setup, polling, storage selection or auto-delete
	}
	return ErrLocked
}

func matches(p profile, d Device) bool {
	if p.iface != 2 && p.iface != 3 {
		return false
	} // exclude diagnostic and ECM interfaces
	a, b := p.layout, d
	// Enumeration address may change; physical location and full layout must match.
	a.Address = 0
	b.Address = 0
	if !reflect.DeepEqual(a, b) {
		return false
	}
	for _, i := range d.Interfaces {
		if i.Number != p.iface || i.Alternate != 0 || i.Class != 255 || i.Subclass != 0 || i.Protocol != 0 {
			continue
		}
		in, out := 0, 0
		for _, e := range i.Endpoints {
			if e.Attributes&3 != 2 {
				continue
			}
			if e.Address == p.in && e.Address&0x80 != 0 {
				in++
			}
			if e.Address == p.out && e.Address&0x80 == 0 {
				out++
			}
		}
		return in == 1 && out == 1
	}
	return false
}

// Exact commands, not prefixes; no semicolons, whitespace, settings or SMS reads.
func AllowedQuery(cmd string) bool {
	switch cmd {
	case "AT", "AT+CPIN?", "AT+CSQ", "AT+CREG?", "AT+CEREG?", "AT+COPS?", "AT+QCCID", "AT+CIMI", "AT+CGSN", "AT+CGMR", "AT+QNWINFO", `AT+QCFG="usbnet"`, `AT+QCFG="usbcfg"`, "AT+CMGF?", "AT+CPMS?", "AT+CNMI?":
		return true
	}
	return false
}
func (s *Session) Query(ctx context.Context, cmd string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !AllowedQuery(cmd) {
		return "", errors.New("command is not an approved read-only query")
	}
	if s.closed || !s.claimed {
		return "", ErrLocked
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	resp, err := s.transport.Query(ctx, cmd)
	if err != nil {
		return "", s.failLocked(err)
	}
	if err = completeOK(resp); err != nil {
		return "", s.failLocked(err)
	}
	return resp, nil
}
func completeOK(resp string) error {
	lines := strings.Split(strings.ReplaceAll(resp, "\r", ""), "\n")
	last := ""
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		last = l
		if l == "ERROR" || strings.HasPrefix(l, "+CME ERROR") || strings.HasPrefix(l, "+CMS ERROR") {
			return errors.New("modem rejected query")
		}
	}
	if last != "OK" {
		return errors.New("query did not end with OK")
	}
	return nil
}
func (s *Session) failLocked(cause error) error { return errors.Join(cause, s.closeLocked()) }
func (s *Session) Close() error                 { s.mu.Lock(); defer s.mu.Unlock(); return s.closeLocked() }
func (s *Session) closeLocked() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.transport == nil {
		return nil
	}
	var releaseErr error
	if s.claimed {
		releaseErr = s.transport.Release(s.iface)
		s.claimed = false
	}
	closeErr := s.transport.Close()
	if releaseErr != nil || closeErr != nil {
		return fmt.Errorf("release/close: %w", errors.Join(releaseErr, closeErr))
	}
	return nil
}
