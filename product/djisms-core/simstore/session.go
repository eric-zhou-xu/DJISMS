package simstore

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"time"
)

type session struct {
	t          *Transport
	store      *archive.Store
	id         string
	frame      framer
	seq        int
	directSeen int
	noticeSeen int
	sourceHash string
	onDirect   func(Direct, string) error
}

func (s *session) save(kind string, v any) error {
	b, e := archive.Canonical(v)
	if e != nil {
		return e
	}
	s.seq++
	s.sourceHash, e = s.store.SaveSource(fmt.Sprintf("sim-storage/%s/%06d/%s", s.id, s.seq, kind), b)
	return e
}
func (s *session) read(ctx context.Context) error {
	wait, e := boundedWait(ctx, 250*time.Millisecond)
	if e != nil {
		return e
	}
	buf := make([]byte, 512)
	d, e := s.t.b.readDiagnostic(buf, wait)
	n := 0
	if d.CountValid && d.ActualBytes != nil {
		n = int(*d.ActualBytes)
	}
	when := time.Now().UTC().Format(time.RFC3339Nano)
	if x := s.save("usb_read", archive.M{"diagnostic": d, "valid_hex": hex.EncodeToString(buf[:n]), "observed_utc": when, "stream_offset": s.frame.offset}); x != nil {
		s.frame.fault = x
		return x
	}
	if n > 0 {
		s.frame.when = when
		parseErr := s.frame.feed(buf[:n])
		for s.directSeen < len(s.frame.directs) {
			v := s.frame.directs[s.directSeen]
			if s.onDirect != nil {
				if x := s.onDirect(v, s.sourceHash); x != nil {
					s.frame.fault = x
					return x
				}
			}
			s.directSeen++
		}
		if parseErr != nil {
			return parseErr
		}
	}
	if e != nil && !errors.Is(e, errTimeout) {
		s.frame.fault = e
		return e
	}
	return nil
}
func (s *session) query(parent context.Context, c Command) (Response, error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	if !s.frame.boundary() {
		return Response{}, errors.New("unresolved input boundary")
	}
	if s.frame.active != nil {
		return Response{}, errors.New("active response")
	}
	if e := s.frame.begin(c); e != nil {
		return Response{}, e
	}
	wire, e := c.wire()
	if e != nil {
		return Response{}, e
	}
	if e = s.save("out_intent", archive.M{"command": wire, "hex": hex.EncodeToString([]byte(wire + "\r"))}); e != nil {
		return Response{}, e
	}
	if e = s.t.b.write(c, 250*time.Millisecond); e != nil {
		return Response{}, e
	}
	if e = s.save("out_succeeded", archive.M{"command": wire}); e != nil {
		return Response{}, e
	}
	for i := 0; i < 1024; i++ {
		if e = s.read(ctx); e != nil {
			return Response{}, e
		}
		if s.frame.active.Done && s.frame.boundary() {
			r, e := s.frame.finish()
			if e != nil {
				return r, e
			}
			if e = s.save("response", r); e != nil {
				return r, e
			}
			if r.Error != "" {
				return r, fmt.Errorf("device rejected optional SIM command: %s", r.Error)
			}
			return r, nil
		}
	}
	return Response{}, errors.New("bounded query exhausted")
}
