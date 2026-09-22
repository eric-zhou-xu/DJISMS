package storageguard

import (
	"errors"
	"github.com/iniwex5/vohive/product/djisms-core/archive"
)

func Check(s *archive.Store) error {
	opened, e := s.LatestEvent("sim_management_open_intent")
	if e != nil {
		return e
	}
	closed, e := s.LatestEvent("sim_management_closed")
	if e != nil {
		return e
	}
	if opened != nil && (closed == nil || opened["session"] != closed["session"]) {
		return errors.New("unfinished SIM transport session; explicit reconciliation required")
	}

	a, e := s.LatestEvent("sim_selection_intent")
	if e != nil {
		return e
	}
	if a == nil {
		return nil
	}
	b, e := s.LatestEvent("sim_selection_restored")
	if e != nil {
		return e
	}
	if b == nil || a["session"] != b["session"] {
		return errors.New("SIM storage selection has an unfinished durable intent; receive/delete remain blocked pending reconciliation")
	}
	return nil
}
