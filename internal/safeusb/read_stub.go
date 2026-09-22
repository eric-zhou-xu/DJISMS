//go:build !darwin || !cgo

package safeusb

import "errors"

type SystemReader struct{}

func (SystemReader) Snapshot() (Snapshot, error) {
	return Snapshot{}, errors.New("cached IOKit descriptors require macOS and cgo; use an offline fixture")
}
