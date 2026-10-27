# QuickAnalyser

**by Soji** · MIT License · Copyright (c) 2026 Soji

Portable Windows package for local offline review of device diagnostic snapshots. Product recognition is optional enrichment: readable-log analysis works for unknown products. No telemetry or cloud upload by default.

## Release status

| Item | Status |
|---|---|
| Label | QA-v1.0-2026 — **development / unreleased** (see `VERSION` and `BUILD_INFO.json`) |
| Platform (this ZIP) | Windows amd64 |
| Delivery | Portable extract-and-run — not an installer |
| License / copyright | MIT / Copyright (c) 2026 Soji |

Commit, dirty flag and build time are recorded in `BUILD_INFO.json` and **About**. They are **not** part of the ZIP filename. A dirty or unknown-commit build must not be treated as a stable published release.

## User guides (this folder)

Guides are maintained under `docs/user/` in the source tree and shipped beside the executable:

- [QUICK_START.md](QUICK_START.md) — extract, launch, storage, first analysis, Quit
- [USER_GUIDE.md](USER_GUIDE.md) — storage, tabs, Highlights, Log Viewer, history, limits
- [TROUBLESHOOTING.md](TROUBLESHOOTING.md) — symptoms, Diagnostics/DEBUG, bug-report template

## Package contents

| Item | Purpose |
|---|---|
| `QuickAnalyser.exe` | Application |
| `QUICK_START.md` / `USER_GUIDE.md` / `TROUBLESHOOTING.md` | User documentation |
| `README.md` | This file |
| `VERSION` | Version label |
| `BUILD_INFO.json` | Build identity (version, commit, dirty, build time, status) |
| `LICENSE` | MIT license text |
| `THIRD_PARTY_NOTICES.md` | Dependency notices |
| `licenses/` | Third-party license texts |
| `SHA256SUMS.txt` | Payload file checksums |

The ZIP checksum sidecar (`.zip.sha256`) sits **beside** the ZIP, not inside it.

## How to run

1. Extract the **entire** ZIP into a folder you choose.
2. Double-click `QuickAnalyser.exe`.
3. Choose a working-storage location when prompted.
4. Use **Quit** in the application to stop the local engine (closing a browser tab alone may not).

You do **not** need Go, Node.js, Python, or a separate database for the packaged runtime.

Flags (optional, when started from a terminal): `--data-dir <path>`, `--portable`, `--no-browser`, `--log-level info|debug`, `--debug-minutes 15`.

## Storage and privacy

Executable location and working-data location are independent. Uploads, scratch, evidence, SQLite, searches, exports and diagnostics stay under the selected working root. Review diagnostics and support bundles before sharing; they may contain operational paths and errors.

## License

MIT License · Copyright (c) 2026 Soji · Developer signature: by Soji.

See `LICENSE`, `THIRD_PARTY_NOTICES.md` and `licenses/`.
