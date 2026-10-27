# QuickAnalyser

**by Soji** · MIT · Copyright (c) 2026 Soji

Local, offline review of device snapshot archives with evidence-backed product attributes, suspicious log highlights, file search and browser log viewing.

## Status and downloads

**Development preview — not a stable release.** Planned version: QA-v1.0-2026. Portable Windows amd64 ZIPs are supplied separately from source. A source download does not contain the executable. Review BUILD_INFO.json and SHA256 checksums for the exact package identity.

## Run the portable package

Extract the entire Windows ZIP and launch QuickAnalyser.exe. Choose a writable local working-data folder. No separate Go, Node, Python, SQLite service or installer is needed for packaged execution. The engine serves its embedded UI on local loopback and opens a browser. Use Quit to stop it. Clean-host/platform/resource qualification is still incomplete.

- [Quick Start](docs/user/QUICK_START.md)
- [User Guide](docs/user/USER_GUIDE.md)
- [Troubleshooting and DEBUG](docs/user/TROUBLESHOOTING.md)
- [Requirements](docs/user/SYSTEM_REQUIREMENTS.md)

## Features and limits

- tar, tar.gz and tgz snapshots, including bounded nested archives.
- Generic product-independent log analysis; optional NX and SGVE profile enrichment.
- Named logs/rotations, zero-event log inventory, suspicious snippets and physical-line references.
- Full retained-source search and bounded continuous viewing.
- Local selected storage, saved analyses, reuse, manual cleanup and diagnostic bundles.

Detections are investigation leads, not confirmed incidents. Partial coverage is explicit. Some extensionless key=value SGVE logs remain classified as configuration. Native Linux and a complete browser/platform matrix remain unverified. Do not claim the preview works on every Windows machine.

## Build from source (Windows)

Install Go compatible with go.mod, Git for provenance, and PowerShell. Node is used for JavaScript regression tests; it is not an end-user runtime dependency. Initial module downloads need internet. CI specifies its tool versions.

```powershell
go mod download
.\scripts\verify.ps1
.\scripts\package_windows.ps1
.\scripts\test_package_payload.ps1
```

Output: dist/QA-v1.0-2026-windows-amd64-dev.zip. Build scripts record actual commit/dirty/time; release mode requires a clean committed candidate. Respect local application-control policy if generated test executables are blocked; do not disable protection globally.

## Privacy, contributions and license

Snapshots stay local by default; review exports and support bundles before sharing. Never put customer logs, configurations, credentials or private device identifiers in public issues or source history. Included testdata/sanitized files are synthetic/redacted test fixtures.

See [Contributing](CONTRIBUTING.md), [Security reporting](SECURITY.md), [MIT license](LICENSE), [Third-party notices](THIRD_PARTY_NOTICES.md) and [Changelog](CHANGELOG.md).
