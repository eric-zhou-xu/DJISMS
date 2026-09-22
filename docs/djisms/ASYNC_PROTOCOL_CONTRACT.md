# Command/response and asynchronous SMS contract — R8

Authority: 2026-09-22 Autonomous Engineering Authorization. Prior candidates,
STOPs, source hashes, journal and raw archive remain immutable. This is a new
candidate; no older candidate's hardware Gate evidence transfers.

## Protocol model

The existing single serial IF2 owner and fixed read-only status / receive /
per-index purge plans remain. No setters, reset, bulk deletion, send, alternate
port, takeover or expanded IPC command is introduced.

A shared PDU-mode asynchronous subframe parser separates CMT (SMS-DELIVER), CDS
(status report) and CBM (broadcast evidence) from command echo/body/terminator.
It validates header syntax, hex octets, declared length, SMSC extent and TPDU
message type. A body cannot supply an AT echo or OK; nested or malformed bodies
fail closed. Header/body bytes can span USB reads and occur before echo, during
responses or after OK. The status plan finishes an observed trailing subframe
before issuing another command. CDS/CBM remain typed preserved evidence; they
are not mislabeled as SMS-DELIVER or assigned modem storage indices.

Known single-line URCs are classified independently, including registration
URCs distinct from n,stat query replies. Unknown lines fail closed. Unprefixed
identity replies use the audited Baiwang/QDC507 grammar (and existing Quectel/
EG25 fixtures), so a printable unknown event cannot become a manufacturer/model
response. Original bytes and decoded evidence are not normalized or rewritten.

Status work is bounded at 10 seconds, 1,024 reads and 65,536 bytes per request,
120 seconds for the fixed plan, and 128 PDU subframes per query. These bounds
allow fragmented SMS and durable recording, without unbounded draining.
Timeout diagnostic counts are never treated as valid bytes. Incomplete or
unknown protocol state remains STOP. Storage changes during purge retain the
existing strict inventory/deletion reconciliation and fail-closed behavior.
A direct no-slot SMS may be archived during purge without changing its target.

The protocol basis is ETSI TS 127 005 V17.0.0, sections 3.1 and 3.4.1, and the
existing device's preserved transport evidence. PDU-mode length excludes the
SMSC address for SMS and covers the PDU for CBS. This is a framing reference,
not a claim that Quectel's other hardware exactly matches QDC507 behavior.
https://www.etsi.org/deliver/etsi_ts/127000_127099/127005/17.00.00_60/ts_127005v170000p.pdf

## Durable handoff and interruption

Raw reads are preserved before classification. Status handoff is derived by
replaying the verified source bytes, never by trusting a reported parsed event.
Its source ID is `status-transport/<session>/<request>/<header-byte-offset>`;
the unchanged archive UID rule hashes that source ID. The intent binds the exact
source hash, source name/sequence/time and frame span. The original PDU and null
storage/index are retained. SIM identity is left unknown until actually proven;
later SIM information is not backfilled into an earlier observation.

Intent -> raw archive -> verify/reconcile -> decoder/message -> completion is
journaled. Each stage is replayable and idempotent; source ID reuse with altered
bytes is rejected. A crash after raw source preservation but before creating the
intent is handled by replaying explicitly marked status sessions. A newly
observed fact can produce a new deterministic receipt through this pipeline;
no receipt, UID or provenance is inserted manually. Historical STOP 16793's PDU
uses this same proven path, not a one-off database repair.

Receive/purge direct observations retain `product/<session>/direct/<eventID>`
and the original metadata contract. Timestamped raw reads and explicit transport
session records allow replay of direct event ordinals after interruption.
Direct handoff intents and completion are durable. Complete frames before a
later fault may be preserved; unknown tails are never guessed. Incomplete
transport frames require review and do not permit new device operations.

## Explicit status recovery

The engineering-only `djisms-status-recover REVIEW.json` command is not bundled
or exposed in consumer IPC. It binds the exact latest STOP, final query/source,
session host proof and compiled candidate identity. Full archive verification,
zero pending destructive/archive state, supported physical identity, unowned
non-ECM interfaces, SIP and fresh bound HTTPS remain prerequisites.

Recovery replays the known request prefix and completes source-bound handoffs.
On the same exact attachment it only reads the outstanding fixed query's reply;
it never retransmits OUT. A persisted write intent with uncertain return can be
continued without claiming the original write succeeded; exact echo/body/OK
must be observed. On a proven new attachment, it explicitly records that the
old read-only request's response belongs to the disposed attachment; it does
not replay that command against a stale device identity. Fresh normal startup
still runs the full plan and receive inventory checks. No new attachment is
created by this recovery command.

A durable completed continuation may be reconciled after an interruption;
previous byte prefixes must remain identical. Only after fresh host comparison
and handoff completion does a candidate-bound authorization get appended.
Old STOPs remain. Another STOP, another candidate, a changed source, unclassified
prefix or unresolved deletion cannot consume the grant. Missing status-session
closure at startup blocks new queries and requires explicit continuation.

## Validation and Gate

Tests cover every byte split and interleaving position, terminal/echo spoofing,
known and unknown URCs, malformed lengths/types, trailing partial frames,
read-only continuation at every prefix, no retransmission after uncertain OUT,
source/UID immutability, exactly-once projection, durability faults across
handoff/archive/decode boundaries, and receive/purge direct interleaving.
Full existing race/native/archive/notification/deletion/identity/DNS/sleep tests
remain required. These are offline proofs only. Final Gate still requires the
same frozen candidate's real device checks and three actual system Sleep/Wake
cycles. No simulated wake or earlier-version cycle counts toward that Gate.
