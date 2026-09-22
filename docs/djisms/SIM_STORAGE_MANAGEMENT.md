# SIM storage management contract

This feature is separate from the accepted V1 release. Preserve that release and its evidence. Capability observations apply only to the observed device/firmware/SIM and time. Never infer SM capacity, full behavior or write support from a standard or from ME capacity.

The receive/purge baseline selects ME for all three CPMS roles. Its historical deletion evidence names ME. New SIM management must not redirect that automatic purge to SM. SM deletion is exclusively an explicit, per-operation user action.

Discovery starts with an immutable query-only plan: CPMS?, CNMI?, CPMS=?, CMGL=?, CMGR=?, CMGD=?, CMGW=?. No CPMS setter, CMGD execution or CMGW write is permitted in this probe. Preserve transport before classification; preserve attributed terminal rejection as a capability result, not success support. An unrecognized response remains a protocol failure. Query OK alone is insufficient to prove execution semantics.

A later SM operation requires exclusive ownership, a saved original CPMS configuration and a durable operation record before any selection change. It must restore and read back the original configuration before receive resumes. An interrupted selection must block receive/purge until settings are reconciled; startup must never assume ME. SM inventory must be bound to physical identity, SIM identity, exact PDU bytes and index.

A destructive plan is a short-lived immutable manifest of selected SM records, their exact PDUs/hashes and reliable permanent archive links. The UI presents count, index, sender, time, preview, archive status and uncertainty. No confirmation means no command. No trustworthy archive mapping means deletion is blocked. Each deletion rereads the complete SM inventory, validates the plan and archive, preserves before evidence, deletes only its one selected index, then rereads all SM records. Only target absence plus all other exact records unchanged proves deletion. Any mismatch or uncertainty stops the batch. No body-similarity matching, bulk deletion, automatic SIM deletion, automatic retry of unknown deletion, or mutation of historical Mac originals.

Write-to-SIM is discovery only in this task. An advertised test-command response does not prove received-PDU preservation or successful reread. Never fill a real SIM to test full behavior without a separate authorized nondestructive fixture/environment; retain unknown if historical evidence cannot prove it.

## Observed device and delivery scope
2026-09-22 actual query: ME 0/23, SM 0/50. CPMS test advertises ME/MT/SM/SR for all three roles. Baseline and restored settings are ME/ME/ME with CNMI 2,1,0,0,0. User explicitly requires mem3=ME. The helper changes mem1 only, checks mem2/mem3=ME during every inventory, and never offers SM automatic purge. SM-full impact remains unknown; mem3=ME alone does not prove firmware behavior when SM is full.

CMGL=4 returned an empty SM inventory. CMGR=? and CMGW=? returned only OK; neither proves nonempty SM reading, deletion, or received-PDU write semantics. No real SMS was generated, written or deleted for this feature. Exact read/delete and interleaving workflows have deterministic offline tests; real nonempty SM remains unvalidated. Reading may mark messages read; reported status is not a reconstructed pre-read state.

## Deployment and interruption
The UI bundles a separate typed SIM helper and the byte-identical accepted R8 Core. A read-only archive check before Core startup blocks unfinished management sessions, selection restoration, unknown deletes and unreviewed Core STOPs. Every transport session has a durable open intent and classified/closed checkpoint. Direct delivery is preserved before parsing, then explicitly handed off to the existing receipt/materialization/notification queue with durable intent/completion. A crash with unclosed state remains fail closed for explicit evidence reconciliation, not automatic clear-STOP.
