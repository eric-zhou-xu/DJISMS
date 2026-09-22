// Package usbrestore implements one host-side reenumeration, never a modem reset.
package usbrestore

import (
	"errors"
	"github.com/iniwex5/vohive/product/djisms-core/discovery"
)

type Result struct {
	Stage           int    `json:"stage"`
	OpenCode        uint32 `json:"open_code"`
	ReenumerateCode uint32 `json:"reenumerate_code"`
	CloseCode       uint32 `json:"close_code"`
	Attempted       bool   `json:"attempted"`
}

func validate(d discovery.Device) error {
	if e := discovery.Validate(d); e != nil {
		return e
	}
	for _, f := range d.Interfaces {
		if f.Number < 4 && f.Owner != "" {
			return errors.New("non-ECM interface is occupied; no host reenumeration")
		}
	}
	return nil
}
