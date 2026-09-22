# Host-side sleep recovery — Development RC build 12

User instruction 2026-09-22 Final Engineering Closeout explicitly approves at most one USBDeviceReEnumerate(0) per safely closed sleep checkpoint. The pre-existing general reset/detach prohibition remains for module resets, forceful takeover, composition changes and unrelated operations. This specific host reenumeration is scoped by that new instruction.

## Evidence
Pinned upstream Mac-lte-guard 890d3c0cd5141c54f88864e0b2e61e740ba4ea4f145 uses ordinary USBDeviceOpen followed by USBDeviceReEnumerate(0). Its author reports EC25 results, not QDC507 results. Historical pre-build-12 evidence only: independent source implementation recovered our preserved QDC507 2ca3:4006 / AppleUserECM / Baiwang sleep failure on 2026-09-22: ordinary UID, open/re-enumerate/close all return 0, a new USB registry node, IPv4 and bound HTTPS 200, original app/core resumed its durable inventory checkpoint. This does not yet prove repeated integrated sleep E2E.

Apple IOUSBLib.h documents whole-device client/driver termination and re-enumeration. It temporarily interrupts ECM as well as the AT interfaces. It does not invoke a modem AT reset or write NV/firmware/composition. Neither root nor capture/release entitlement is requested. The original third-party binary, installer, update, hooks and scripts are not shipped or executed.

## Runtime rule
Only a durable checkpoint created after an idle receive session has closed for system sleep can enable recovery. Legacy checkpoints do not enable it. On wake the existing network verification runs first. The same registry and USB location must still be present, all descriptors must match the reviewed profile, the device must be unique, and vendor AT interfaces must be unoccupied. Only an explicit media-inactive observation lasting at least five seconds triggers an attempt; an Internet/HTTPS failure with active media does not.

The journal intent is durable before DeviceOpen. There is one ReEnumerate(0) attempt per sleep checkpoint, with a second identity/descriptor/ownership check after a non-seizing open. Any occupied interface, open failure, API failure or ambiguous result stops; there is no escalation, retry, shell call, CFUN, networksetup, driver install or persistent service. The intent survives process failure and prevents another call after restart.

The driver tree may briefly disappear during the expected re-enumeration. Discovery waits are bounded by a persisted 45-second recovery deadline. Normal discovery/profile validation, SIP and bound HTTPS checks, status plan and preserved-inventory reconciliation must all pass before receive resumes and the checkpoint is closed. A successful API return alone does not mark recovery successful. A NoDevice return on closing the invalidated old user client is recorded as an expected API lifecycle case; other close errors fail closed.

## Validation
Hardware-free C mocks link CoreFoundation only, without IOKit. They exercise no/multiple devices, wrong registry/location/revision/configuration/descriptor, occupied IF2 or wrong ECM driver, state change after open, busy open, API error, both close error classes, root rejection and success. Go tests cover sleep-only guards, continuous inactive grace, journal failure before mutation, intent-only crash replay, unknown result replay, recovered deadline cancellation, spontaneous link healing and the serialized run loop through discovery churn to inventory reconciliation. Exact commands/results are archived with the build.

No machine-specific registry ID, location, interface name, UID or user path is embedded in runtime behavior. USB profile identity is the approved supported-device contract. Automatic recovery is implemented inside djisms-core; no helper process or additional runtime dependency is installed.
