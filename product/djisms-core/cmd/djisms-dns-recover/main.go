// Engineering-only reviewed recovery. Not distributed in the consumer app.
package main

import (
	"context"
	"encoding/json"
	"github.com/iniwex5/vohive/product/djisms-core/runtime"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	b, e := os.ReadFile(os.Args[1])
	if e != nil {
		fail(e)
	}
	var review runtime.DNSRecoveryReview
	if e = json.Unmarshal(b, &review); e != nil {
		fail(e)
	}
	c, e := runtime.New("", nil)
	if e != nil {
		fail(e)
	}
	e = c.AuthorizeDNSRecovery(context.Background(), review)
	closeErr := c.Close()
	if e != nil {
		fail(e)
	}
	if closeErr != nil {
		fail(closeErr)
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"authorized": true, "receive_only_start": true, "stop_fingerprint": review.StopFingerprint})
}
func fail(e error) { json.NewEncoder(os.Stderr).Encode(map[string]any{"error": e.Error()}); os.Exit(1) }
