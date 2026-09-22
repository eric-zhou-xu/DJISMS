# Evidence reuse across candidate builds

Trusted PASS evidence is inherited across candidate builds unless an identified
code, contract, environment, or provenance change invalidates its applicability.
A new build or candidate alone does not invalidate prior PASS evidence.

Before testing, record a change impact analysis linking each affected requirement
to the relevant change and existing evidence. For every proposed retest, name the
affected requirement and explain why prior evidence is insufficient. Run the
smallest sufficient set of impact-based targeted tests. Record executed commands,
exit status, evidence references and hashes, evidence reused, tests skipped, and
the reason for each reuse, skip, or invalidation. Missing or untrusted evidence
must be recorded as such; never manufacture PASS results.

Physical Gates are release-milestone tests, requiring an explicitly applicable
milestone and authorization within the current task. Do not repeat physical
replug, Sleep/Wake, real SMS, ECM/network, purge, notification, or full regression
tests merely because a new candidate exists. This infrastructure bootstrap
authorizes no Physical Gates or hardware tests; its validation is limited to
offline document scope and governance checks.

Preserve original facts, files, raw evidence, and source provenance before any
transformation. UID, hash, identity, and deduplication contracts remain frozen
unless a task explicitly authorizes a reviewed migration. Test PASS alone does
not establish data correctness: reconcile relevant inventories, counts,
identities, and hashes deterministically, with no unexplained loss or duplication.
Keep raw run evidence in the host-managed append-only store outside disposable
workspaces, and publish only sanitized evidence with traceable references.

For this one-file documentation change, record the base and head commits and
verify that only `docs/autonomy/evidence-reuse.md` differs. No product behavior,
UID/hash/source provenance, runtime, or data changes are authorized. The package
supplies no prior PASS evidence, so none can be claimed as reused. Product and
physical tests are skipped because product behavior is unaffected and physical
testing is unauthorized, respectively. No merge or deployment is authorized.

Publish the commit, PR, and machine-readable Issue evidence before transitioning
to technical review. A real ChatGPT technical correction must revise the same
task and be processed on the same branch and PR. Until that review and rework are
demonstrated, the autonomous review/continuation loop remains unverified;
documentation or initial publication alone is not proof of an operational loop.
