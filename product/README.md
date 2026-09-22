# DJISMS V1 Development RC source

A native Apple Silicon menu bar application. `DJISMS.app` contains the Swift/AppKit UI and a Go/C helper linked to system IOKit and SQLite. The installed app needs no Terminal, Homebrew, Python, Go, elevated privileges, custom driver, or firmware change.

## Ordinary use

1. Install `DJISMS.app` in Applications or your user's Applications folder and open it.
2. Connect exactly one supported DJI 4G module. Leave the existing Apple ECM network configuration intact. The app verifies the hardware and existing 4G connection before receiving.
3. Allow macOS notifications if desired. Denying notifications does not stop permanent local archiving. The menu bar opens local history, search and settings.
4. Verified modem records are individually reread, matched to archived PDU bytes and cleared. Turning off automatic cleanup stops new individual operations; an operation already in progress finishes reconciliation.
5. Closing the window keeps receiving. Quitting stops the helper after graceful reconciliation. There is no installed login item or persistent daemon.

Raw messages, SQLite, immutable journal, and transport provenance live in the current user's `~/Library/Application Support/DJISMS/`. The app has no cloud/NAS/AI integration. Do not remove or edit this folder to troubleshoot. Binary, malformed and incomplete messages retain their original bytes. An unresolved deletion or safety inconsistency stops automatic processing across restarts.

Uninstall by quitting and moving only `DJISMS.app` to Trash. Permanent messages remain. Deleting the permanent archive is deliberately not part of uninstall.

## Compatibility

Build target: Apple Silicon, macOS 14 or later. Actual platform acceptance is recorded in the release candidate report; the minimum deployment target alone is not proof of testing on every macOS version. Only the exact audited `2CA3:4006`, revision `0x0318`, configuration 1 ECM descriptor profile is supported. Active interface 2 must be free; ECM interfaces 4/5 must remain owned by AppleUserECM. Modem storage ME capacity 23 and the verified PDU/CNMI/CSMS settings are required. Other compositions fail closed. USB location, registry identity, network interface and IPv4 are discovered dynamically.

Only one device is operated at a time. Multiple devices, unknown profiles, missing ECM IPv4, denied IF2 access or changed network/SIP state produce an explicit status. No fallback driver, takeover, reset, configuration change or permission weakening exists. A small TLS-verified request to Apple's connectivity test endpoint is bound to the discovered ECM interface; SMS content is never sent over this connection.

## Developer build

An audited Go toolchain, Apple Command Line Tools, Python 3 for the build manifest, and the pinned reviewed SMS dependency are build-time requirements only:

```sh
DJISMS_GO=/path/to/go product/packaging/build.sh
```

Output: `build/djisms/<unique-build-id>/DJISMS.app` with `build-manifest.json`. `DJISMS_BUILD_DIR` can select another developer output directory. Build uses `-trimpath`, an explicit deployment target, Apple frameworks, system SQLite and local ad-hoc signing. This is a development build, not a formally distributable notarized release.

Tests run with `go test -race -tags djisms_native ./product/djisms-core/...`. Native tests use offline IOKit mocks, including rejection of zero/wrong registry identity. Integration seams are private to Go tests; IPC cannot replace a transport, select a raw command, index, path or USB endpoint.

Formal distribution requires an authorized Apple Developer ID identity, notarization, stapling and clean-machine verification. `product/packaging/release.sh` is prepared for that authorized step. No credentials are embedded and the development build does not ask users to disable Gatekeeper or SIP.

## Recovery and version identity

The implemented API is documented in `djisms-core/contracts/README.md`. The App
handles sleep/wake lifecycle, unavailable status and request timeouts. An eligible
idle receive interruption reconnects after fresh identity/settings/inventory/host
verification. Unknown deletes, partial frames and archive inconsistencies remain
locked. Existing archived cache is considered when automatic cleanup is enabled,
even without a new SMS. The UI distinguishes PDU time from archive time and labels
status observation time. Installation and recovery directions are also bundled
and available from the menu bar.

Each build has a unique build ID, bundle build and source digest recorded in the
App, Core hello, startup journal and artifact manifest. See `packaging/README.md`.
Acceptance results belong to the specific frozen binary; working-source tests
are not a claim of same-version physical unplug/sleep/iPhone acceptance.
