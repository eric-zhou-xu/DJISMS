package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"github.com/iniwex5/vohive/product/djisms-core/simstore"
	"io"
	"os"
	"time"
)

func main() {
	if len(os.Args) != 1 {
		os.Exit(2)
	}
	var r simstore.Request
	dec := json.NewDecoder(io.LimitReader(os.Stdin, 16384))
	dec.DisallowUnknownFields()
	if e := dec.Decode(&r); e != nil {
		json.NewEncoder(os.Stdout).Encode(simstore.Result{Error: e.Error()})
		return
	}
	s, e := archive.Open("")
	if e != nil {
		json.NewEncoder(os.Stdout).Encode(simstore.Result{Error: e.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	out, e := simstore.Run(ctx, s, r)
	if e != nil {
		out.Error = e.Error()
	}
	if e = s.Close(); e != nil {
		out.Restored = false
		out.Error = fmt.Sprint(e)
	}
	json.NewEncoder(os.Stdout).Encode(out)
}
