# 2026-09-21 Development RC scope amendment

The user's accepted status audit and subsequent continuous-development request
supersede the historical single-ME1 and dry-run-only stage limits below.
`CONTINUOUS_WORK_PACKAGE.txt` authorizes serialized archive-verified per-index
automatic cleanup; unknown results never authorize retries. The implemented V1
API and recovery boundaries are in `product/djisms-core/contracts/README.md`.
V1 uses one device, private child-process pipes, durable outbox plus query APIs;
it does not claim a device selector, XPC, external purge-preview tokens or a
cursor-replay subscription. Developer ID/notarization are formal-release gates,
not prerequisites to develop a Development RC. Acceptance must distinguish
same-build hardware results, offline tests, historical evidence and unavailable
external environments. The user's waived new multipart live test stays waived.

---

# DJISMS V1 — frozen product contract

## 2026-09-18 distributable-app amendment (latest, authoritative)

The final product is a standalone standard macOS App for other Apple Silicon Mac users.
Ordinary users require no Terminal, Codex, Homebrew, Go, root/sudo, SIP changes, custom
modem driver, or DJI firmware changes. The architecture is explicitly split into
`djisms-core` and `DJISMS.app`; see DISTRIBUTABLE_APP_CONTRACT.md for responsibilities,
typed API, lifecycle, portability and release acceptance criteria.

Core owns USB/AT, SMS/PDU, permanent archive, SQLite, lifecycle, safe individual purge
and reconciliation. App owns menu bar, Notification Center, history/search, device
status and settings; UI cannot submit raw AT or bypass core safety. The earlier
minimal-GUI scope below is superseded by these specific App features.

Release requires Apple Developer ID signing and notarization. Ad-hoc signing is only
a development option. All runtime paths resolve for the current user; no Eric paths,
fixed UID/USB location/en6/SIM/operator in the product. Audited hardware identity and
descriptor contracts govern matching, with safe hot-plug and multiple-device handling.

The ongoing one-shot Gate is separately authorized only for ME index 1 and the exact
approved PDU hash (FIRST_ME1_PURGE_GATE.md). This amendment grants no further deletes,
background installation, publication or external account operation.

## Original archive foundation contract (historical stage limits below)

Frozen from the user's explicit 2026-09-18 instruction. This document takes precedence
over earlier receive-only scope for design and offline development. It does **not**
authorize the first hardware purge or a persistent background installation.

1. The DJI modem is a temporary SMS inbox. The Mac is the permanent local fact store.
2. Authoritative root: `~/Library/Application Support/DJISMS/`. No cloud, NAS, AI,
   external account, OTP-specific retention, expiry or automatic local deletion.
3. SQLite stores indexes, readable text and lifecycle state. Immutable raw archives
   retain exact original PDU and acquisition metadata; journal retains receive,
   archive, eligibility, attempted delete and observed outcome facts.
4. Every stored record/fragment must complete **Preserve → Verify → Reconcile**
   before archive eligibility. Eligibility is not user authorization and is not a
   guarantee the old modem index still refers to this record.
5. Before each future actual delete, freshly verify selected storage, index and raw
   PDU hash under exclusive device ownership. Only `AT+CMGD=<index>` exists in the
   design. Bulk flags, wildcard deletion, storage-wide purge and retries after an
   ambiguous outcome are forbidden permanently.
6. Incomplete multipart fragments, binary messages, Class0 and decode failures keep
   their original facts. Fragment/binary records may be eligible after successful
   archival; decode errors remain held for review; direct events without a modem
   slot are explicitly not applicable to modem deletion.
7. Successfully archived readable messages generate native macOS Notification Center
   banners with sender + preview. Notification failure, dismissal or disappearance
   never changes the permanent archive. No complex GUI in V1.
8. This stage implements/audits model, schema, raw archive, journal, crash recovery,
   reuse guards, notification design and offline archive-and-purge tests, and
   generates a real **Dry Run** for the current full modem. **No real deletion.**
9. First real purge awaits separate explicit approval after a concrete reviewed plan.
10. Future hot-plug auto-receive is designed and audited only. No LaunchAgent,
    login item, daemon or other persistent installation without separate approval.

Any future runtime must archive acquired bytes directly inside this root before
decoding. The old audit Desktop directories are historical evidence, not the new
product's storage location. Tests use isolated temporary local roots and synthetic
messages; they never operate on the user's permanent archive or device.
