# Explicit closed-receive DNS recovery, candidate r7

Authorized by the 2026-09-22 Fully Authorized Task Package. Build 12 and all prior candidates remain historical artifacts. This contract changes no UID, hash, archive schema, typed IPC, AT whitelist, TLS policy, deletion rule or sleep recovery limit.

The external engineering-only `djisms-dns-recover REVIEW.json` command opens the archive exclusively and performs the ordinary full archive/journal/projection verification. It does not start the receiver or issue USB commands. No recovery command is bundled in the consumer app or exposed over IPC.

Only the exact fixed-endpoint DNS `no such host` failure is eligible. Other DNS errors, HTTPS/TLS errors, receive failures, destructive failures and incomplete evidence remain STOP. Review binds the latest STOP payload fingerprint, session ID, session-end and preceding successful host source hashes, explicit operator review, compiled build ID and source-tree hash.

The journal must show the contiguous closed receive session immediately before STOP. Replay of preserved USB bytes must prove the existing ten-query empty-session plan, no arrivals, no reads/deletes, empty ME inventory, final settings reconciliation, complete response boundaries and successful close. Recorded responses must equal raw replay. Missing facts, unknown facts, nonempty inventory and partial closure fail closed. The historical forensic label is preserved, not upgraded or rewritten; the new replay proof is a separate fact.

Fresh native discovery must identify exactly one supported descriptor profile at the historical physical USB location, with all non-ECM interfaces unoccupied. Historical and current registry IDs and USB addresses are recorded separately as attachment epochs; none are rewritten. The existing identity validator remains unchanged. Two fresh native host captures require SIP enabled and ordinary certificate-validated HTTPS bound to the actual ECM interface; three discovery snapshots must reconcile exactly. No DNS override, proxy, TLS bypass or reenumeration is allowed by this command.

No unresolved deletion or pending archive staging may exist. Only after all checks pass does the command preserve its proof, durably disable automatic purge, then append `dns_receive_recovery_authorized`. The old STOP is never removed or changed. Startup accepts only the matching authorization and proof hash on the same compiled candidate. A later STOP or another candidate cannot consume this grant. Ordinary startup still performs full device/settings/inventory checks. No background auto-clear or generic clear-STOP path exists.

An interrupted authorization remains fail closed unless the final grant itself became durable. A proof/preferences-only interruption may be explicitly reviewed again; no USB operation has occurred. After a successful grant, runtime activity prevents a second authorization against the old context. Archive crash recovery remains the existing journal-authoritative replay.

Validation: raw historical empty-session fixture replay; missing/mutated command/read/response/end rejection; candidate/STOP/host binding; ownership and discovery races; HTTPS/SIP failures; cancellation; durability fault injection; existing race/native/receive/archive/notification/purge/recovery regression suite. Offline tests never count as physical Gate cycles.

Final Gate requires this candidate's independent three physical system Sleep/Wake cycles, complete per-checkpoint reconciliation and at-most-one reenumeration, plus all existing same-candidate live message/notification/purge/replug/iPhone checks. Evidence from previous builds is context only. Until those checks pass, V1 remains STOP.
