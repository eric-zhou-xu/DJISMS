// Explicit engineering recovery; never launches receive or issues AT.
package main

import (
	"encoding/json"
	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"github.com/iniwex5/vohive/product/djisms-core/discovery"
	"github.com/iniwex5/vohive/product/djisms-core/host"
	"github.com/iniwex5/vohive/product/djisms-core/shutdownproof"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	b, e := os.ReadFile(os.Args[1])
	if e != nil {
		panic(e)
	}
	var r shutdownproof.Review
	if e = json.Unmarshal(b, &r); e != nil {
		panic(e)
	}
	s, e := archive.Open("")
	if e != nil {
		panic(e)
	}
	defer s.Close()
	if e = shutdownproof.Verify(s, r); e != nil {
		panic(e)
	}
	facts, e := s.SourceFacts("session/" + r.Session + "/")
	if e != nil {
		panic(e)
	}
	var old host.Snapshot
	found := false
	for _, f := range facts {
		if f.Hash == r.Host {
			if e = archive.Decode(f.Data, &old); e != nil {
				panic(e)
			}
			found = true
		}
	}
	if !found {
		panic("host proof missing")
	}
	ds, e := discovery.Snapshot()
	if e != nil {
		panic(e)
	}
	fresh, e := discovery.Single(ds)
	if e != nil {
		panic(e)
	}
	if e = discovery.Reconcile(old.Device, fresh); e != nil {
		panic(e)
	}
	if e = shutdownproof.Authorize(s, r); e != nil {
		panic(e)
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"authorized": true, "scope": shutdownproof.Scope, "AT_commands": 0})
}
