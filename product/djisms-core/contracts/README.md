# DJISMS Development RC protocol 1

This is the implemented contract as of 2026-09-21. It supersedes the earlier
unimplemented XPC/preview-token API sketch for V1, under the user's continuous
productization and individual automatic-purge authorization. Historical design
text remains in `docs/djisms/DISTRIBUTABLE_APP_CONTRACT.md` for provenance.

## Transport and ownership

The App starts one bundled, ordinary-user `djisms-core` child. Newline-delimited
UTF-8 JSON travels only on inherited stdin/stdout pipes. No TCP listener, public
socket, daemon, login item or privileged service exists. EOF causes graceful core
shutdown. The core exclusively locks the current user's archive. The UI never
constructs AT, opens USB, writes the database, or supplies filesystem paths.

Requests require `version: 1`, a nonempty `id` of at most 80 bytes, and a `method`.
Maximum request size is 16,384 bytes. Unknown fields/methods, trailing JSON,
unsupported versions and out-of-range fields are rejected. A response echoes the
request ID, with either `data` or `error` and a stable `error_code`. Codes are
`InvalidRequest`, `OperationFailed`, `ArchiveUnavailable`, and `CoreStopped`.
Device lifecycle `phase` values are stable machine-readable status categories;
errors are not classified by scraping localized display text.

| Method | Input | Result / behavior |
|---|---|---|
| `status` | none | current state and durable preferences |
| `messages` | `search` ≤1,024 bytes, `limit` 1–200 (0 defaults to 100), `offset` 0–1,000,000 | bounded previews, IDs, part counts, PDU time when known and archive time |
| `message` | `message_id` | verified full body for an existing 64-character ID |
| `notifications` | none | at most 32 pending outbox entries, each archive-verified |
| `notification_result` | `message_id`, result enum, detail ≤1,024 bytes | idempotent terminal delivery result; never changes raw archive eligibility |
| `preferences` | `notifications`, `auto_purge` booleans | durable change; false→true purge requests a graceful boundary even without a new SMS |
| `archive_summary` | none | receipt/message/event counts, journal head, unresolved deletes and pending files |
| `power` | `prepare_sleep` or `awake` | lifecycle hint; pauses new operations and requests graceful receive close; never cancels/retries unknown deletion |
| `shutdown` | none | serialized safe close; no forced termination of in-flight deletion |

The `hello` event includes protocol, version, build ID and source-tree digest.
`state`, `history_changed`, `preferences`, and `stopped` events are advisory
notifications. They are not a durable stream/cursor API. The App re-queries current
state/history; the notification outbox remains in the archive across process
failures. A lost event cannot delete history. Requests with callbacks time out in
the UI after 15 seconds and show unavailable/error state instead of keeping a
false current-state claim. Timeout does not kill or retry a device command.

## V1 scope decisions

- Exactly one audited device may be connected. Zero, multiple, unsupported and
  owned-interface cases are explicit statuses; there is no device-selector UI.
- Start/stop reception is tied to the owned process and serialized lifecycle.
  Registry identity and per-connection nonce are generated/checked by the core.
- There is no external purge endpoint or raw index/AT API. An internal single-use
  decision binds a permanent receipt, fresh exact storage/index/PDU, current
  inventory, current host verification and durable intent. Continuous automatic
  cleanup was explicitly authorized; a per-message UI confirmation would add a
  different workflow. Unknown results remain locked across App restarts.
- Notification failure or lack of permission affects only notification delivery.
  A local test notification, if used during validation, is not a received SMS.
- Status samples carry their observation time. Disconnected, waiting, checking,
  sleep and safety-stop states do not present old LTE or capacity as current.

## Recovery boundary

After an idle receive-only IOKit abort/no-device/not-attached error, recovery is
allowed only when no query, queued CMTI or partial frame exists, valid source
bytes were durably preserved, native references were released and session-end
persistence succeeded. No additional OUT is sent on that failed session.
A durable checkpoint preserves exact prior inventory hashes. Reacquisition
checks identity/descriptors, current network/SIP/TLS, fixed settings and a fresh
snapshot. Existing slots must still match before normal operation resumes.

Sleep requests close at a complete receive boundary and reconcile settings. A
completed receive-only close may defer the host check until wake, with explicit
recorded evidence and the same inventory checkpoint. Purge reconciliation is
never deferred. An active query error, partial frame, archive error, ownership
failure, changed inventory, failed reconciliation or any purge failure remains a
safety stop. This rule is deliberately narrower than "retry every USB error".

Published staging duplicates are retained under `recovery/` only if identical to
an existing permanent file. Unknown/partial staging bytes are retained in place
and block device operations. No local facts are deleted by recovery.

Physical removal close semantics: a receive-only close reporting NoDevice is
accepted only after unconditional native release and a fresh discovery scan
proving the pinned old registry identity absent. The summary separately records
`released_after_device_removal`; it does not claim USBInterfaceClose returned OK.
An idle NotResponding read requires that removal proof in addition to the full
framing boundary. A retained device, failed discovery, other close error, active
query, partial input or failed evidence write remains a safety stop. Purge and
status transports do not use this exception.

The engineering-only recovery tool can review the pre-fix empty-inventory
NotResponding + NoDevice-close failure by replaying all contiguous hash-verified
session facts. It requires exactly six completed initial queries, an empty
snapshot, no pending messages, the exact terminal failure, a new physical device
identity and two fresh host checks. It preserves the original stop, starts with
automatic purge disabled and cannot approve a later failure. It is not bundled
or exposed through IPC.
