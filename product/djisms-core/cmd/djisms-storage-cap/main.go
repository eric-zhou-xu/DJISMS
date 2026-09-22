package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"github.com/iniwex5/vohive/product/djisms-core/discovery"
	"github.com/iniwex5/vohive/product/djisms-core/storagecap"
	"os"
	"time"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) != 1 {
		return fmt.Errorf("no arguments accepted")
	}
	s, e := archive.Open("")
	if e != nil {
		return e
	}
	defer s.Close()
	ds, e := discovery.Snapshot()
	if e != nil {
		return e
	}
	if len(ds) != 1 {
		return fmt.Errorf("one device required")
	}
	if e = discovery.Validate(ds[0]); e != nil {
		return e
	}
	id := time.Now().UTC().Format("20060102T150405.000000000Z")
	if e = s.Event("storage_capability_probe_opened", "", archive.M{"id": id, "device": ds[0], "policy": "fixed-readonly-no-setters-no-delete-no-write"}); e != nil {
		return e
	}
	t := storagecap.NewNative(ds[0].RegistryID, ds[0].Location)
	if e = t.Connect(context.Background()); e != nil {
		return e
	}
	n := 0
	r, e := t.ExecutePlan(context.Background(), func(r storagecap.Report) error {
		n++
		raw, x := json.Marshal(r)
		if x != nil {
			return x
		}
		_, x = s.SaveSource(fmt.Sprintf("storage-capability/%s/%06d", id, n), raw)
		return x
	})
	json.NewEncoder(os.Stdout).Encode(r)
	s.Event("storage_capability_probe_closed", "", archive.M{"id": id, "closed": r.CloseSucceeded, "success": e == nil})
	return e
}
