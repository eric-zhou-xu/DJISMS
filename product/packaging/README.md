# Building and distributing DJISMS

Developer build requirements: Apple Silicon macOS with Apple Command Line Tools,
Go 1.26.3, and Python 3 for build-manifest generation. None is a runtime dependency.
The app bundles its native core and uses only macOS frameworks/system SQLite.

`DJISMS_GO=/absolute/path/to/go product/packaging/build.sh` builds in a new
`build/djisms/<unique-build-id>/` directory and refuses to overwrite an app.
Optional variables: `DJISMS_BUILD_DIR`, `DJISMS_BUILD_ID`, `DJISMS_VERSION`, and
`DJISMS_BUNDLE_BUILD`. The frozen acceptance build records the exact values.
Use the same values in a separate output directory for a reproducibility check.

`build-manifest.json` binds every app file to source SHA-256 entries, source-tree
digest, exact version/build identifiers, Go/Swift/SDK versions, architecture,
deployment target and signing status. Compilation fails if sources changed while
building. `-trimpath` and `-buildvcs=false` prevent developer paths and unrelated
repository state from becoming the binary's provenance. The source snapshot and
manifest, not the dirty historical repository HEAD, are authoritative.

App/Core/Info.plist/hello/journal share a build ID and source digest. Ad-hoc signing
with hardened runtime is a development option, not Developer ID or notarization.
System Gatekeeper acceptance and cross-machine support are separate evidence.

`release.sh` performs inside-out Developer ID signing, notarization, stapling and
Gatekeeper assessment only when actual credentials and release authorization are
available. Re-signing changes binary hashes; generate and retain a separate formal
release manifest after that process. Never overwrite the development RC evidence.

No release package includes the engineering receive-recovery command, test tools,
Terminal launchers, raw AT entry points, Python, Go, Homebrew, driver, LaunchAgent,
login item, daemon or runtime dependency on the source tree.

A normal uninstall removes only DJISMS.app after a graceful quit. User archives
are deliberately retained. Installation/permissions/recovery directions are
bundled at `Contents/Resources/USER_GUIDE.txt` and available from the App menu.
