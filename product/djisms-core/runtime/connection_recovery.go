package runtime

import (
	"errors"
	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"time"
)

type recoverySlot struct {
	Receipt  string `json:"receipt"`
	Hash     string `json:"binary_sha256"`
	TextHash string `json:"text_sha256"`
}
type recoveryCheckpoint struct {
	ID         string               `json:"id"`
	Location   uint32               `json:"location,omitempty"`
	Sleep      bool                 `json:"sleep,omitempty"`
	RegistryID uint64               `json:"registry_id"`
	Reason     string               `json:"reason"`
	Slots      map[int]recoverySlot `json:"slots"`
}

func (c *Core) saveConnectionRecovery(registry uint64, reason string, location ...uint32) error {
	r := recoveryCheckpoint{ID: nonce(), RegistryID: registry, Reason: reason, Slots: map[int]recoverySlot{}}
	if len(location) == 1 {
		r.Location = location[0]
		r.Sleep = reason == sleepIdleReason
	}
	for idx, saved := range c.recoveryInventory {
		r.Slots[idx] = recoverySlot{saved.receipt, saved.hash, saved.textHash}
	}
	b, e := archive.Canonical(r)
	if e != nil {
		return e
	}
	var event archive.M
	if e = archive.Decode(b, &event); e != nil {
		return e
	}
	if e = c.Store.Event("receive_connection_interrupted", "", event); e != nil {
		return e
	}
	c.recoveryID = r.ID
	c.recoveryCheckpoint = &r
	c.recoveryInactiveSince = time.Time{}
	return nil
}
func (c *Core) loadConnectionRecovery() error {
	event, e := c.Store.LatestEvent("receive_connection_interrupted")
	if e != nil || event == nil {
		return e
	}
	raw, e := archive.Canonical(event)
	if e != nil {
		return e
	}
	var r recoveryCheckpoint
	if e = archive.Decode(raw, &r); e != nil {
		return e
	}
	if r.ID == "" || r.Slots == nil {
		return errors.New("invalid connection recovery checkpoint")
	}
	done, e := c.Store.LatestEvent("receive_connection_restored")
	if e != nil {
		return e
	}
	if done != nil && done["id"] == r.ID {
		return nil
	}
	c.recoveryInventory = map[int]record{}
	for idx, v := range r.Slots {
		if idx < 0 || idx > 22 || len(v.Receipt) != 64 || len(v.Hash) != 64 || len(v.TextHash) != 64 {
			return errors.New("invalid recovery inventory")
		}
		c.recoveryInventory[idx] = record{v.Receipt, v.Hash, v.TextHash}
	}
	c.recoveryID = r.ID
	c.recoveryCheckpoint = &r
	c.recoveryInactiveSince = time.Time{}
	return nil
}
