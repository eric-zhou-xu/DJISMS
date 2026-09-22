package main

import (
	"context"
	"encoding/json"
	"github.com/iniwex5/vohive/product/djisms-core/ipc"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	signal.Ignore(syscall.SIGPIPE)
	if len(os.Args) != 1 {
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server, e := ipc.New("", os.Stdout)
	if e != nil {
		json.NewEncoder(os.Stdout).Encode(ipc.Response{Version: ipc.Version, Event: "fatal", Error: e.Error(), ErrorCode: "ArchiveUnavailable"})
		os.Exit(1)
	}
	if e = server.Run(ctx, os.Stdin); e != nil {
		json.NewEncoder(os.Stdout).Encode(ipc.Response{Version: ipc.Version, Event: "fatal", Error: e.Error(), ErrorCode: "CoreStopped"})
		os.Exit(1)
	}
}
