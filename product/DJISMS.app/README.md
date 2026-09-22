# DJISMS.app native UI source

`Sources/main.swift` implements the AppKit menu bar, native notifications, history
search/pagination, device status, preferences, sleep/wake hints and bundled help.
This source directory is not itself the installable app; the packaging build
assembles the executable, native core, Info.plist, guide and license resources.

The UI calls only the private typed core API. It sends no AT, writes no archive
tables and does not infer deletion success. Ordinary use needs no Terminal or
development environment. All runtime data belongs to the current user's
Application Support/DJISMS directory. See ../README.md and
../djisms-core/contracts/README.md for build and protocol details.
