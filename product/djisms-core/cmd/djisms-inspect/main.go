// Read-only engineering diagnostic. Not bundled into the consumer application.
package main

import (
	"context"
	"encoding/json"
	"github.com/iniwex5/vohive/product/djisms-core/discovery"
	"github.com/iniwex5/vohive/product/djisms-core/host"
	"os"
)

func main() {
	ds, e := discovery.Snapshot()
	out := map[string]any{"devices": ds}
	if e == nil && len(ds) == 1 {
		e = discovery.Validate(ds[0])
	}
	if e == nil && len(os.Args) == 2 && os.Args[1] == "--host" && len(ds) == 1 {
		var h host.Snapshot
		h, e = host.Capture(context.Background(), ds[0])
		out["host"] = h
	}
	if e != nil {
		out["error"] = e.Error()
	}
	json.NewEncoder(os.Stdout).Encode(out)
	if e != nil {
		os.Exit(1)
	}
}
