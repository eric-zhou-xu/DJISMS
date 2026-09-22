package runtime

import (
	"context"
	"github.com/iniwex5/vohive/product/djisms-core/discovery"
	"github.com/iniwex5/vohive/product/djisms-core/host"
	"github.com/iniwex5/vohive/product/djisms-core/purge"
	"github.com/iniwex5/vohive/product/djisms-core/receive"
	"github.com/iniwex5/vohive/product/djisms-core/status"
	"github.com/iniwex5/vohive/product/djisms-core/usbrestore"
)

type statusTransport interface {
	Connect(context.Context) error
	ExecutePlan(context.Context, func(status.Report) error) (status.PlanReport, error)
}
type receiveTransport interface {
	Connect(context.Context) error
	Listen(context.Context, receive.Config, receive.Sink) (receive.Summary, error)
}
type purgeTransport interface {
	Connect(context.Context) error
	Purge(context.Context, purge.Config, purge.Sink) (purge.Summary, error)
}

// Private dependency seam is only set by in-package tests, never through IPC.
type dependencies struct {
	snapshot      func() ([]discovery.Device, error)
	capture       func(context.Context, discovery.Device) (host.Snapshot, error)
	status        func(discovery.Device) statusTransport
	receive       func(discovery.Device) receiveTransport
	purge         func(discovery.Device) purgeTransport
	mediaInactive func(discovery.Device) (bool, error)
	reenumerate   func(discovery.Device, func() error) (usbrestore.Result, error)
}

func nativeDependencies() dependencies {
	return dependencies{snapshot: discovery.Snapshot, capture: host.Capture,
		mediaInactive: usbrestore.MediaInactive, reenumerate: usbrestore.ReEnumerate,
		status:  func(d discovery.Device) statusTransport { return status.NewNative(d.RegistryID, d.Location) },
		receive: func(d discovery.Device) receiveTransport { return receive.NewNative(d.RegistryID, d.Location) },
		purge:   func(d discovery.Device) purgeTransport { return purge.NewNative(d.RegistryID, d.Location) },
	}
}
