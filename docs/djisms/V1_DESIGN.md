# Latest architecture amendment

The distributable product architecture in `DISTRIBUTABLE_APP_CONTRACT.md` is now authoritative.
The Python archive code and machine-specific one-shot Gate below are development/audit
reference implementations, not the shipping runtime. Their existing safety/data contracts
are preserved. `djisms-core/` and `DJISMS.app/` define separate product boundaries.

# DJISMS V1 archive, recovery and purge design

## Scope and executable boundary

`tools/djisms` is a Python standard-library reference implementation of the local
archive and SQLite projection, plus a concrete in-memory purge simulator. It has
**no live deletion transport**. `execute_live` always refuses. The frozen IF2 V2
reader remains unchanged; this stage uses it only for a fresh inventory capture
whose raw acquisition directory is inside the permanent DJISMS root. The original
full receive implementation is not silently relabeled a production hot-plug service.

```
~/Library/Application Support/DJISMS/
  inbox.sqlite3             # WAL + FULL + fullfsync + checkpoint_fullfsync
  raw/<receipt-id>.json     # immutable exact PDU + original metadata envelope
  journal/000000000001.json # immutable, ordered SHA256-chain lifecycle events
  sources/<sha256>.bin      # byte-for-byte original acquisition bundles/journal prefixes
  acquisition/<session>/   # new frozen-reader byte journal, snapshots and reconciliation
  plans/<plan-sha256>.json  # immutable Dry Run plans, no implicit permission
  recovery/                # preserved derived DB / interrupted-write evidence
  .writer.lock             # exclusive process lock, not a background service
```

Only current-user files (0600) and directories (0700), no symlink archive paths,
local APFS/HFS+ on macOS, cloud-provider directories rejected. No application network
API. Security against another process running as the same user/root is not claimed;
hashes detect inconsistency, they are not signatures against a malicious file owner.

## Data model and identity

The executable SQL schema is `tools/djisms/djisms/schema.sql`. `receipts` are acquisition
observations, not a count of unique human SMS messages. They retain source ID,
explicit device key, USB connection identity, storage/index (nullable for direct
events), exact raw file hash, exact PDU ASCII hash, decoded PDU byte hash, original
metadata, archive state, decode state and delete state. Repeated snapshots preserve
distinct observations; repeated cumulative exports within one source stream import
idempotently. A new source event with identical PDU is still a separate fact.

`messages` and `message_parts` are derived logical messages and memberships. A stored
multipart fragment is an independent receipt and archive eligibility unit. Complete
validated decoder groups materialize readable messages; incomplete groups preserve
partial membership, not invented missing text. Existing audited PDU decoder remains
the source of concat key validation (sender/reference width/reference/total/PID/DCS/
UDH/time window). This importer never guesses groups from a shared reference alone.

`events` mirrors immutable journal files. `notifications` is a retryable outbox.
`purge_attempts` distinguishes intent, confirmed delete, failure, unknown outcome,
and read-only recovery after ambiguity. A sender/OTP label never controls retention.

## Preserve → Verify → Reconcile

1. Acquire original byte journal in the permanent root before parsing. On complete
   PDU, prepare an envelope containing exact PDU spelling, full original metadata,
   source occurrence identity and host time. Direct Class0 has null storage/index.
2. Write an exclusive temporary file; flush, fsync and macOS F_FULLFSYNC. Atomically
   link it to its immutable final name, fsync directory, then remove only the
   temporary link. Never overwrite an existing raw fact. Interrupted temporary bytes
   are retained; they grant no eligibility.
3. Append an immutable hash-chained `raw_preserved` event with the same barriers.
   Only after its durable publication, commit the corresponding SQLite transaction.
4. Re-read the raw file; compare file/PDU/metadata hashes with the SQLite projection;
   append/commit `archive_verified`. Decode is a subsequent journaled derivative.
5. Reconcile raw identity, storage coordinates and projection; append/commit
   `archive_reconciled`. Only reconciled, valid stored receipts in explicitly
   supported decode states can be candidates. Binary and incomplete fragments are
   retained permanently and are eligible; direct events have no delete target;
   malformed/unknown/decode-error receipts are held.

Journal events are separate immutable files rather than a mutable JSONL tail. This
avoids truncating lifecycle evidence after a torn write. Contiguous sequence and
previous-event hash are checked before recovery proceeds. F_FULLFSYNC failures,
disk-full, journal gaps, missing raw, mismatched SQLite state or unknown schema stop
eligibility; do not keep receiving indefinitely into RAM while calling it archived.

## Crash recovery

| Crash boundary | Recovery | Delete consequence |
|---|---|---|
| Before raw publication | Keep `.pending-*`; no receipt claimed | no eligibility |
| Raw published, no journal | Verify complete raw envelope; journal recovered fact | unknown decode held until rebuilt |
| Journal published, SQLite not committed | Replay journal transaction | no double insertion |
| SQLite committed, caller did not receive success | Repeat source occurrence is idempotent | no duplicate delete permission |
| Decode incomplete | Keep raw; rebuild decoded derivative later | held if decode unknown/error |
| Before delete after durable intent | Read-only reconciliation | never replay old CMGD |
| Modem deleted but host lost response | state unknown; re-read inventory | absent does not invent historical OK |
| Same index now holds another PDU | archive new occupant; mark reuse | never delete new occupant under old plan |
| Notification submitted but journal result missing | resubmit stable identifier | archive unaffected; exact-once display not claimed |

SQLite is rebuildable by moving the **closed** DB/WAL/SHM set into recovery storage
and replaying journal, never discarding raw/journal. No automatic destructive repair
is attempted. A missing committed journal tail detected by SQLite is fatal. On full
machine rollback/restoration, do a fresh inventory and invalidate all old delete
plans; hashes alone cannot establish freshness against wholesale rollback.

## Dry Run and future first purge

The real plan binds the latest complete, reconciled **closed** inventory snapshot,
selected ME storage, device key, connection registry instance, capacity/use count,
each index, archived receipt ID, raw file hash and PDU hashes. It exposes each
proposed `AT+CMGD=<index>` as data and contains `live_deletion_authorized=false`.
Plan creation emits no AT command. A plan cannot be used after a new USB connection
without new observation and revalidation, even if the modem model is identical.

The future approved executor must itself undergo an independent fixed-command/native
audit and offline testing before use. For each candidate, hold the single writer
and exclusive IF2 ownership, check connection and selected store, CMGR the specific
index, durably preserve that fresh read, verify archive, compare byte-level PDU hash,
durably record intent, then send exactly one index-only CMGD. Record raw response and
reconcile occupancy/content afterwards. Unknown results stop the batch; no retry.

AT SMS offers no atomic compare-and-delete. A device/external actor changing an
occupied slot between the final read and CMGD is a residual TOCTOU risk; exclusive
host ownership and no other modem writers are prerequisites, not a mathematical
guarantee. An identical-PDU replacement is indistinguishable, but its immediate
pre-delete acquisition is also durably saved. Do not claim to have solved an atomic
modem primitive the hardware does not provide.

The simulator exercises this lifecycle and can only accept the concrete in-memory
`SimulatedModem`; no duck-typed hardware adapter is accepted. It is not a permission
backdoor. Actual execution is unavailable in the current CLI.

## Native notification design

After archival reconciliation, readable standalone/complete multipart messages
create notification outbox rows. Do not notify partial text as a complete SMS.
Binary/decode-error records remain queryable but do not invent readable previews.
Historical imports suppress notifications to avoid replaying old OTPs. Future live
arrivals use `notify=True`. OTPs and normal messages have the same permanent policy.

The Swift helper uses `UNUserNotificationCenter` local notifications, sender title
and a 160-character preview. Preview travels by stdin JSON, not shell or argv.
Stable message ID links the banner to the permanent DB record; no remote push, URL,
reply action or script is executed from SMS text. Notification permission is checked
on every submission. Explicit `--authorize` is reserved for user setup; no automatic
permission prompt during import. Denied/Focus-hidden/dismissed notifications do not
delete, expire or invalidate the archive. Requested banner style and actual display
remain controlled by macOS/user settings. The helper is compiled locally for review,
not installed or run to request permission during this stage.

## Future hot-plug service audit (design only)

Run one non-root user worker, no daemon/root/SIP changes. Future signed LaunchAgent
would start a local foreground executable with fixed archive path and ListenOnly /
DryRun mode by default, acquire `.writer.lock`, recover all raw/journal first, discover
one matching physical device, verify USB profile and ECM ownership, then attach IF2.
Attach failures use bounded backoff; detach closes and invalidates connection tokens.
No persistent modem configuration changes, no bulk deletion, no arbitrary AT.

Use IOKit device arrival/removal notifications rather than launching a job per SMS.
On detach during read, preserve partial bytes and stop that connection; on uncertain
delete, recovery is read-only. SIGTERM triggers durable checkpoint/close. Do not make
an Exit/Restart loop a mechanism for retrying ambiguous deletion. Idle service policy,
signed packaging, user approval, install/uninstall and permission UX require a later
review. **No plist is placed in ~/Library/LaunchAgents, no launchctl/bootstrap/login
item, no daemon or startup registration is performed now.**

## Primary references

- [SQLite WAL](https://www.sqlite.org/wal.html): FULL syncs each commit.
- [SQLite PRAGMA](https://www.sqlite.org/pragma.html): synchronous/fullfsync/checkpoint_fullfsync.
- [Apple notification authorization](https://developer.apple.com/documentation/usernotifications/asking-permission-to-use-notifications).
- [Apple UserNotifications](https://developer.apple.com/documentation/usernotifications/).
- Darwin SDK `sys/mount.h`: local filesystem identity; `fcntl.h`: F_FULLFSYNC.
- Existing reviewed `docs/safety/SMS_LIVE_V2.md` for frozen receive-only IF2 transport.
