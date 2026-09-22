package runtime

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/iniwex5/vohive/product/djisms-core/archive"
	"github.com/iniwex5/vohive/product/djisms-core/buildinfo"
	"github.com/iniwex5/vohive/product/djisms-core/discovery"
	"github.com/iniwex5/vohive/product/djisms-core/host"
	"github.com/iniwex5/vohive/product/djisms-core/status"
)

func statusFixture(t *testing.T) (*Core, *session, status.Report, archive.SourceFact) {
	t.Helper()
	c, d := testCore(t)
	s := &session{core: c, device: d, id: strings.Repeat("c", 32), inventory: map[int]record{}}
	if e := c.Store.Event("status_session_opened", "", archive.M{"session": s.id}); e != nil {
		t.Fatal(e)
	}
	if e := s.Save("host_before", host.Snapshot{Device: d, SIP: "enabled", HTTPS: true}); e != nil {
		t.Fatal(e)
	}
	m := smsFixture(t)
	wire := fmt.Sprintf("+CMT: ,%d\r\n%s\r\n", m.TPDULength, m.PDU)
	n := uint32(len(wire))
	r := status.Report{Policy: "readonly-status-demux-v2", RequestID: 1, Interface: 2, Command: "AT+CGMI", PlannedOutHex: hex.EncodeToString([]byte("AT+CGMI\r")), WriteAttempted: true, WriteSucceeded: true, StopReason: "unsupported command/result mode or multiline body", Reads: []status.ReadEvidence{{Index: 1, Phase: "after_write", Diagnostic: status.ReadDiagnostic{ReturnCode: 0, CountValid: true, ActualBytes: &n, RawSize: n}, ValidHex: hex.EncodeToString([]byte(wire))}}}
	if e := s.Save("status_query", r); e != nil {
		t.Fatal(e)
	}
	facts, e := c.Store.SourceFacts("session/" + s.id + "/")
	if e != nil {
		t.Fatal(e)
	}
	return c, s, r, facts[len(facts)-1]
}
func TestStatusHandoffSourceIdentityAndExactlyOnceReplay(t *testing.T) {
	c, s, r, f := statusFixture(t)
	for i := 0; i < 3; i++ {
		if e := c.stageStatus(s.id, f, r); e != nil {
			t.Fatal(e)
		}
	}
	intents, _ := c.Store.EventsOfKind("status_handoff_intent")
	done, _ := c.Store.EventsOfKind("status_handoff_complete")
	if len(intents) != 1 || len(done) != 1 {
		t.Fatal("duplicate handoff")
	}
	id := handoffID(s.id, 1, 0)
	receipt, e := c.Store.Receipt(archive.Hash([]byte(id)))
	if e != nil {
		t.Fatal(e)
	}
	if receipt["storage"] != nil || receipt["storage_index"] != nil {
		t.Fatal("fabricated stored location")
	}
	messages, e := c.Store.Messages("", 200, 0)
	if e != nil || len(messages) != 1 {
		t.Fatal(messages, e)
	}
	// Change the byte content while retaining the same claimed observation ID.
	r.Reads[0].ValidHex = strings.Replace(r.Reads[0].ValidHex, "0041", "0042", 1)
	r.RequestID = 2 // a distinct request is distinct provenance; it cannot reuse ID 1
	frames, e := status.ReplayFrames(r)
	if e != nil || len(frames) != 1 {
		t.Fatal(e)
	}
	var h statusHandoff
	if e = decodeObject(intents[0], &h); e != nil {
		t.Fatal(e)
	}
	h.Frame.PDU += "00"
	if e = c.completeStatusHandoff(h); e == nil {
		t.Fatal("invented frame accepted")
	}
}
func TestStatusHandoffDurabilityFaults(t *testing.T) {
	for _, point := range []string{"journal_durable:status_handoff_intent", "raw_durable", "journal_durable:raw_preserved", "journal_durable:archive_reconciled", "journal_durable:decoded", "journal_durable:message_materialized", "journal_durable:status_handoff_complete"} {
		t.Run(point, func(t *testing.T) {
			c, s, r, f := statusFixture(t)
			root := c.Store.Root
			c.Store.Fault = func(p string) error {
				if p == point {
					return errors.New("injected interruption")
				}
				return nil
			}
			if e := c.stageStatus(s.id, f, r); e == nil {
				t.Fatal("fault not reached")
			}
			if e := c.Close(); e != nil {
				t.Fatal(e)
			}
			next, e := New(root, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer next.Close()
			if e = next.settleStatusHandoffs(); e != nil {
				t.Fatal(e)
			}
			if e = next.settleStatusHandoffs(); e != nil {
				t.Fatal(e)
			}
			msgs, e := next.Store.Messages("", 200, 0)
			if e != nil || len(msgs) != 1 {
				t.Fatal(msgs, e)
			}
			done, _ := next.Store.EventsOfKind("status_handoff_complete")
			if len(done) != 1 {
				t.Fatal("completion duplicated")
			}
		})
	}
}
func TestHistoricalStatusRecoveryNewEpochAndCandidateBinding(t *testing.T) {
	c, s, r, f := statusFixture(t)
	c.stop(errors.New(r.StopReason))
	stop, _ := c.Store.LatestEvent("core_safety_stop")
	fp, _ := stopFingerprint(stop)
	ob, os := buildinfo.BuildID, buildinfo.SourceTree
	buildinfo.BuildID = "status-fixture"
	buildinfo.SourceTree = strings.Repeat("a", 64)
	defer func() { buildinfo.BuildID = ob; buildinfo.SourceTree = os }()
	facts, _ := c.Store.SourceFacts("session/" + s.id + "/")
	review := StatusRecoveryReview{StopFingerprint: fp, Session: s.id, ReportSource: f.Hash, HostSource: facts[0].Hash, BuildID: buildinfo.BuildID, SourceTree: buildinfo.SourceTree, Note: "Explicit source-bound read-only status continuation review."}
	d := device()
	d.RegistryID++
	c.deps.snapshot = func() ([]discovery.Device, error) { return []discovery.Device{d}, nil }
	c.deps.capture = func(_ context.Context, d discovery.Device) (host.Snapshot, error) {
		return host.Snapshot{Device: d, SIP: "enabled", HTTPS: true}, nil
	}
	if e := c.AuthorizeStatusRecovery(context.Background(), review); e != nil {
		t.Fatal(e)
	}
	if ok, e := c.reviewedStop(stop); e != nil || !ok {
		t.Fatal(ok, e)
	}
	buildinfo.BuildID += "other"
	if ok, _ := c.reviewedStop(stop); ok {
		t.Fatal("cross candidate authorization")
	}
	buildinfo.BuildID = review.BuildID
	c.stop(errors.New("unclassified AT line"))
	next, _ := c.Store.LatestEvent("core_safety_stop")
	if ok, _ := c.reviewedStop(next); ok {
		t.Fatal("new STOP authorized")
	}
}

func TestRawStatusCheckpointBeforeHandoffIntentRecoversWithoutUSB(t *testing.T) {
	c, s, _, _ := statusFixture(t)
	root := c.Store.Root
	if e := c.Close(); e != nil {
		t.Fatal(e)
	}
	next, e := New(root, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	touched := false
	next.deps.snapshot = func() ([]discovery.Device, error) { touched = true; return nil, errors.New("must not touch USB") }
	next.run(context.Background())
	if touched {
		t.Fatal("pending status reissued device operations")
	}
	state, _ := next.Current()
	if state.Phase != "safety_stop" {
		t.Fatal(state)
	}
	messages, e := next.Store.Messages("", 200, 0)
	if e != nil || len(messages) != 1 {
		t.Fatal(messages, e)
	}
	pending, e := next.pendingStatusSession()
	if e != nil || pending != s.id {
		t.Fatal(pending, e)
	}
}
func TestStatusRecoveryRejectsUnsafeEvidenceAndHost(t *testing.T) {
	for _, mode := range []string{"stale", "wrong_report", "wrong_host", "other_stop", "unfrozen", "unknown_line", "occupied", "identity", "https", "unfinished_delete", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			c, s, r, f := statusFixture(t)
			if mode == "unknown_line" {
				r.Reads[0].ValidHex = hex.EncodeToString([]byte("+UNKNOWN: bad\r\n"))
				n := uint32(len(r.Reads[0].ValidHex) / 2)
				r.Reads[0].Diagnostic.RawSize = n
				r.Reads[0].Diagnostic.ActualBytes = &n
				s.Save("status_query", r)
				facts, _ := c.Store.SourceFacts("session/" + s.id + "/")
				f = facts[len(facts)-1]
			}
			c.stop(errors.New(r.StopReason))
			stop, _ := c.Store.LatestEvent("core_safety_stop")
			fp, _ := stopFingerprint(stop)
			ob, os := buildinfo.BuildID, buildinfo.SourceTree
			buildinfo.BuildID = "status-negative"
			buildinfo.SourceTree = strings.Repeat("a", 64)
			defer func() { buildinfo.BuildID = ob; buildinfo.SourceTree = os }()
			facts, _ := c.Store.SourceFacts("session/" + s.id + "/")
			review := StatusRecoveryReview{StopFingerprint: fp, Session: s.id, ReportSource: f.Hash, HostSource: facts[0].Hash, BuildID: buildinfo.BuildID, SourceTree: buildinfo.SourceTree, Note: "Explicit source-bound read-only status continuation review."}
			d := device()
			d.RegistryID++
			c.deps.snapshot = func() ([]discovery.Device, error) { return []discovery.Device{d}, nil }
			c.deps.capture = func(_ context.Context, d discovery.Device) (host.Snapshot, error) {
				return host.Snapshot{Device: d, SIP: "enabled", HTTPS: true}, nil
			}
			ctx := context.Background()
			switch mode {
			case "stale":
				review.StopFingerprint = "wrong"
			case "wrong_report":
				review.ReportSource = review.HostSource
			case "wrong_host":
				review.HostSource = review.ReportSource
			case "other_stop":
				c.stop(errors.New("deletion unknown"))
			case "unfrozen":
				review.SourceTree = "unfrozen"
			case "occupied":
				d.Interfaces = append([]discovery.Interface(nil), d.Interfaces...)
				d.Interfaces[2].Owner = "other"
			case "identity":
				d.Location++
			case "https":
				c.deps.capture = func(context.Context, discovery.Device) (host.Snapshot, error) {
					return host.Snapshot{}, errors.New("TLS failed")
				}
			case "unfinished_delete":
				id, e := c.Store.Ingest("fixture", "0000", archive.M{"device_key": "fixture", "connection_id": "fixture", "received_at": "2026-09-22T00:00:00Z", "storage": "ME", "index": 0}, archive.M{"state": "binary"})
				if e != nil {
					t.Fatal(e)
				}
				_, h := archive.PDUHashes("0000")
				if e = c.Store.Event("delete_intent", id, archive.M{"attempt_id": "fixture", "storage": "ME", "index": 0, "pdu_sha256": h, "connection_id": "fixture", "mode": "simulation"}); e != nil {
					t.Fatal(e)
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if e := c.AuthorizeStatusRecovery(ctx, review); e == nil {
				t.Fatal("unsafe authorized")
			}
			grant, _ := c.Store.LatestEvent("status_recovery_authorized")
			if grant != nil {
				t.Fatal("unexpected grant")
			}
		})
	}
}
