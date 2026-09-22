# V1 engineering closeout — build 12

Final authorization: 2026-09-22 Final Engineering Closeout. Scope remains receive, permanent local archive, notification, per-index safe purge. No send/call/eSIM/cloud or UI redesign.

IPC protocol 1 emits `initializing` with phase `archive_verification` before opening and verifying the local archive. `hello` means the typed request service is ready. App polling, history and notification reads wait for hello; lifecycle power/shutdown signals remain deliverable during initialization. Preferences remain disabled until their persisted values arrive. Archive verification is never bypassed and the core is never killed to shorten startup. Existing post-handshake requests retain their 15-second timeout.

Source tests include three independent simulated recovery checkpoints; those do not prove actual system sleep. Final hardware evidence must use this build and separately establish repeated real sleep/wake, per-checkpoint at-most-once recovery, live SMS, notification delivery, purge reconciliation, physical replug and iPhone Internet. No earlier build's hardware PASS transfers to this build.
