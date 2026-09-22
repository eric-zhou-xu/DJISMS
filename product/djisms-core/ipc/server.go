// Package ipc exposes a bounded typed API on private parent/child pipes only.
package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"github.com/iniwex5/vohive/product/djisms-core/buildinfo"
	"github.com/iniwex5/vohive/product/djisms-core/runtime"
	"io"
	"strings"
	"sync"
	"time"
)

const Version = 1

type Request struct {
	Version     int                  `json:"version"`
	ID          string               `json:"id"`
	Method      string               `json:"method"`
	Search      string               `json:"search,omitempty"`
	Limit       int                  `json:"limit,omitempty"`
	Offset      int                  `json:"offset,omitempty"`
	MessageID   string               `json:"message_id,omitempty"`
	Result      string               `json:"result,omitempty"`
	Detail      string               `json:"detail,omitempty"`
	Preferences *runtime.Preferences `json:"preferences,omitempty"`
	Power       string               `json:"power,omitempty"`
}
type Response struct {
	Version   int    `json:"version"`
	ID        string `json:"id,omitempty"`
	Event     string `json:"event,omitempty"`
	Data      any    `json:"data,omitempty"`
	Error     string `json:"error,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}
type Server struct {
	mu      sync.Mutex
	encoder *json.Encoder
	Core    *runtime.Core
	cancel  context.CancelFunc
}

func New(root string, out io.Writer) (*Server, error) {
	s := &Server{encoder: json.NewEncoder(out)}
	if err := s.encoder.Encode(Response{Version: Version, Event: "initializing", Data: map[string]any{"phase": "archive_verification"}}); err != nil {
		return nil, err
	}
	core, e := runtime.New(root, func(kind string, v any) { s.send(Response{Version: Version, Event: kind, Data: v}) })
	if e != nil {
		return nil, e
	}
	s.Core = core
	return s, nil
}
func (s *Server) send(r Response) {
	s.mu.Lock()
	e := s.encoder.Encode(r)
	cancel := s.cancel
	s.mu.Unlock()
	if e != nil && cancel != nil {
		cancel()
	}
}
func Parse(raw []byte) (Request, error) {
	var r Request
	if len(raw) > 16384 {
		return r, errors.New("request too large")
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if e := d.Decode(&r); e != nil {
		return r, e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return r, errors.New("trailing JSON")
	}
	if r.Version != Version || r.ID == "" || len(r.ID) > 80 {
		return r, errors.New("protocol version or request ID invalid")
	}
	switch r.Method {
	case "status", "message", "messages", "notifications", "notification_result", "preferences", "archive_summary", "shutdown", "power":
	default:
		return r, errors.New("unsupported typed method")
	}
	if len(r.Search) > 1024 || r.Limit < 0 || r.Limit > 200 || r.Offset < 0 || r.Offset > 1000000 || len(r.Detail) > 1024 || len(r.MessageID) > 64 {
		return r, errors.New("request bounds")
	}
	if (r.Method == "power" && r.Power != "prepare_sleep" && r.Power != "awake") || (r.Method != "power" && r.Power != "") {
		return r, errors.New("invalid power lifecycle event")
	}
	return r, nil
}
func (s *Server) Dispatch(r Request) (any, error) {
	switch r.Method {
	case "power":
		return nil, s.Core.SetPower(r.Power == "prepare_sleep")
	case "status":
		state, prefs := s.Core.Current()
		return map[string]any{"state": state, "preferences": prefs}, nil
	case "message":
		return s.Core.Store.Message(r.MessageID)
	case "messages":
		limit := r.Limit
		if limit == 0 {
			limit = 100
		}
		return s.Core.Store.Messages(r.Search, limit, r.Offset)
	case "notifications":
		return s.Core.Store.PendingNotifications()
	case "notification_result":
		return nil, s.Core.Store.NotificationResult(r.MessageID, r.Result, r.Detail)
	case "preferences":
		if r.Preferences == nil {
			return nil, errors.New("preferences missing")
		}
		return nil, s.Core.SetPreferences(*r.Preferences)
	case "archive_summary":
		return s.Core.Store.Summary()
	case "shutdown":
		return map[string]any{"closing": true}, nil
	}
	return nil, errors.New("unsupported method")
}
func (s *Server) Run(parent context.Context, in io.Reader) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	s.Core.Start(ctx)
	s.send(Response{Version: Version, Event: "hello", Data: map[string]any{"protocol": Version, "version": buildinfo.Version, "build": buildinfo.BuildID, "source_tree": buildinfo.SourceTree, "archive": "local-permanent", "signing": "development"}})
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), 16385)
	inputDone := make(chan error, 1)
	go func() {
		for scanner.Scan() {
			if ctx.Err() != nil {
				inputDone <- nil
				return
			}
			r, e := Parse(scanner.Bytes())
			if e != nil {
				s.send(Response{Version: Version, ID: r.ID, Error: e.Error(), ErrorCode: "InvalidRequest"})
				continue
			}
			data, e := s.Dispatch(r)
			reply := Response{Version: Version, ID: r.ID, Data: data}
			if e != nil {
				reply.Error = e.Error()
				reply.ErrorCode = "OperationFailed"
			}
			s.send(reply)
			if r.Method == "shutdown" {
				inputDone <- nil
				return
			}
		}
		inputDone <- scanner.Err()
	}()
	var e error
	select {
	case <-ctx.Done():
	case e = <-inputDone:
	}
	cancel()
	// Core owns graceful stop and reconciliation. No timeout kills its in-flight
	// purge; a parent crash can orphan this user process briefly until it closes.
	closeErr := s.Core.Close()
	s.send(Response{Version: Version, Event: "stopped", Data: map[string]any{"time": time.Now().UTC().Format(time.RFC3339Nano)}})
	return errors.Join(e, closeErr)
}
