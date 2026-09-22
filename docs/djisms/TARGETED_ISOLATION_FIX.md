# Targeted cancellation / optional SIM isolation fix

The accepted R8 and prior feature releases remain untouched. This change has its own Core/helper identity. It does not claim a repeat of V1 Physical Gate.

Root: SIM helper check incorrectly gated every Core launch; automatic SIM backup shut down Core. R8 cancellation during a closed status attempt with no OUT was still escalated to durable safety STOP. These are independent defects: optional-feature coupling plus a pre-existing narrow shutdown classification gap.

Core owns archive/delete/unfinished SM physical-state safeguards, without requiring the optional helper to exist or succeed. UI starts Core independently. SM backup is explicit and temporarily owns the transport; no automatic interruption. A classified echoed modem ERROR/ numeric CMS/CME rejection is preserved and returned as a feature error; restore/readback and close markers permit Core restart. Unknown input, unclosed transport, unproven storage restoration or unknown deletion remain blocking risks.

New shutdown predicate is deliberately narrow: a closed fixed CGMI first-query plan, context canceled, no OUT attempted/succeeded, no response/echo/terminal/async/stream events, and at most a before-write timeout carrying no trustworthy input bytes. After durable closure and audit this is orderly cancellation, not an integrity STOP. Any attempted OUT, input bytes, unrelated error or close failure remains Fail Closed.

Historical STOP is never deleted or edited. An engineering-only command validates the exact STOP fingerprint, final source-bound session, all query snapshots, closed plan, handoff audit, archive/deletion summary and same device attachment via read-only discovery. It appends a separate grant bound to this exact new candidate/source and preserved proof. No AT, TLS/network test, replug or generic clear STOP occurs. A new STOP, candidate mismatch or missing/changed proof rejects the grant. Normal Core startup still enforces receive ME/ME/ME and its existing checks.
