package runtime

import (
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"github.com/iniwex5/vohive/product/djisms-core/receive"
	"strings"
	"testing"
)

func TestReceiveRawCheckpointRestoresDirectWithExistingIdentityContract(t *testing.T) {
	for _, point := range []string{"before_classification", "journal_durable:direct_handoff_intent", "raw_durable", "journal_durable:direct_handoff_complete"} {
		t.Run(point, func(t *testing.T) {
			c, d := testCore(t)
			s := &session{core: c, device: d, id: strings.Repeat("e", 32), inventory: map[int]record{}}
			if e := s.openTransport("receive"); e != nil {
				t.Fatal(e)
			}
			m := smsFixture(t)
			when := "2026-09-22T01:02:03Z"
			head := fmt.Sprintf("+CMT: ,%d", m.TPDULength)
			wire := head + "\r\n" + m.PDU + "\r\n"
			n := uint32(len(wire))
			if e := s.Save("usb_read", archive.M{"observed_utc": when, "valid_hex": hex.EncodeToString([]byte(wire)), "diagnostic": receive.ReadDiagnostic{ReturnCode: 0, RawSize: n, ActualBytes: &n, CountValid: true}}); e != nil {
				t.Fatal(e)
			}
			if point != "before_classification" {
				c.Store.Fault = func(p string) error {
					if p == point {
						return errors.New("power interrupted")
					}
					return nil
				}
				e := s.Direct(receive.Direct{EventID: 1, ObservedUTC: when, Offset: 0, Header: head, HeaderHex: hex.EncodeToString([]byte(head + "\r\n")), TPDULength: m.TPDULength, PDU: m.PDU, PDULineHex: hex.EncodeToString([]byte(m.PDU + "\r\n"))})
				if e == nil {
					t.Fatal("fault not reached")
				}
			}
			root := c.Store.Root
			c.Close()
			next, e := New(root, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer next.Close()
			if e = next.settleDirectTransports(); e != nil {
				t.Fatal(e)
			}
			if e = next.settleDirectTransports(); e != nil {
				t.Fatal(e)
			}
			id := archive.Hash([]byte("product/" + s.id + "/direct/1"))
			raw, e := next.Store.Verify(id)
			if e != nil {
				t.Fatal(e)
			}
			meta := raw["metadata"].(map[string]any)
			if meta["received_at"] != when || raw["raw_pdu"] != m.PDU || meta["storage"] != nil {
				t.Fatal("identity mutated", meta)
			}
			msgs, e := next.Store.Messages("", 200, 0)
			if e != nil || len(msgs) != 1 {
				t.Fatal(msgs, e)
			}
		})
	}
}
