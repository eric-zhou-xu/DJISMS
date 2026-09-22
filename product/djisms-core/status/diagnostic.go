package status

import "fmt"

// Apple IOUSBLib.h: on ReadPipeTO error, size is NOT updated and buffer data is
// invalid. RawSize is evidence of the API output, never proof of received bytes.
type ReadDiagnostic struct {
	ReturnCode  uint32  `json:"iokit_return_code"`
	ReturnHex   string  `json:"iokit_return_hex"`
	OSMessage   string  `json:"iokit_message"`
	RawSize     uint32  `json:"raw_size_parameter"`
	Requested   uint32  `json:"requested_bytes"`
	ActualBytes *uint32 `json:"actual_valid_bytes"`
	CountValid  bool    `json:"byte_count_valid"`
	Category    string  `json:"category"`
	PipeRef     uint8   `json:"pipe_reference"`
	TimeoutMS   uint32  `json:"timeout_ms"`
	ElapsedUS   int64   `json:"elapsed_microseconds"`
	Quiet       bool    `json:"quiet_check_passed"`
}

const ioTimeout uint32 = 0xe00002d6
const usbTransactionTimeout uint32 = 0xe0004051

func classifyRead(rc, raw, requested uint32) ReadDiagnostic {
	d := ReadDiagnostic{ReturnCode: rc, ReturnHex: fmt.Sprintf("0x%08x", rc), RawSize: raw, Requested: requested}
	switch {
	case rc == 0 && raw > requested:
		d.Category = "invalid_size"
	case rc == 0:
		d.CountValid = true
		n := raw
		d.ActualBytes = &n
		if raw == 0 {
			d.Category = "success_empty"
		} else {
			d.Category = "data"
		}
	case rc == ioTimeout || rc == usbTransactionTimeout:
		d.Category = "timeout"
	default:
		d.Category = "error"
	}
	// Physical silence is neither asserted nor used as a send prerequisite.
	d.Quiet = false // Never a proof of physical no-input; not an admission gate.
	return d
}
