# QuickAnalyser — Quick Start

**by Soji** · MIT License · Copyright (c) 2026 Soji

This short guide is for ordinary Windows users of the **portable package** (a ZIP containing `QuickAnalyser.exe`). You do **not** need to install Go, Node.js, Python, or a separate database to run the packaged application.

## Supported platform (this package)

| Item | Value |
|---|---|
| Operating system | Windows |
| Architecture | amd64 (64-bit) |
| Delivery | Portable ZIP — extract and run; not an installer |

Windows amd64 is the primary supported target for this package. Linux builds may exist in the project but are **not** claimed verified by this Quick Start unless a separate native Linux qualification says so.

**Clean-laptop verification** (a machine without development tools) is recorded separately in the release tracker when performed. Do not assume it from this document alone.

## 1. Extract the ZIP

1. Download or copy `QA-v1.0-2026-windows-amd64-dev.zip` (development) or `QA-v1.0-2026-windows-amd64.zip` (approved stable release, when published).
2. Extract the **entire** ZIP into a folder you choose (for example `Documents\QuickAnalyser`).
3. Keep all extracted files together: the executable, guides, `LICENSE`, `BUILD_INFO.json`, `SHA256SUMS.txt`, and `licenses\`.

## 2. Launch

1. Double-click `QuickAnalyser.exe` (or start it from Explorer / a shortcut).
2. The application runs **locally** on your PC. It starts a small local web server on `127.0.0.1` (loopback only) and normally opens your default browser to the UI.
3. If the browser does not open, see `TROUBLESHOOTING.md`. You can also use **Quit** later and relaunch with the same EXE.

No cloud login or activation is required for normal use.

## 3. Choose working storage

On first run (or from **Settings**), choose where QuickAnalyser keeps uploads, scratch files, evidence, search data, and saved analyses:

- **Default user-data location** — under your Windows user profile application data.
- **Choose a working directory** — any local folder you select.
- **Portable: QuickAnalyserData beside the executable** — data next to the EXE.

Confirm with **Use this location**.

Executable location and data location are independent. Prefer a local disk with free space; avoid putting the working root on synced/network folders when possible.

## 4. Analyse a snapshot

1. Open **Upload Snapshot**.
2. Select a supported archive (`.tar`, `.tar.gz`, `.tgz`). Nested archives and gzip text logs are handled within documented limits.
3. Watch **Processing** for stage progress. You can cancel an active analysis when the UI offers **Cancel**.
4. When complete, review **Overview**, **Log Highlights**, **Log Viewer**, and **Configuration**.

Product identity (for example NX or SGVE) is optional enrichment. Unknown products still receive generic readable-log analysis.

## 5. Saved analyses

Use **Previous Analyses** to reopen results you already have under the selected storage root. Open an analysis to view it without re-uploading. Identical-file reuse may apply when the same archive content is uploaded again.

## 6. Quit safely

- Use the application **Quit** button so the local engine stops.
- Closing only the browser tab may leave the local engine running.
- To restart, run `QuickAnalyser.exe` again (only one instance should own a given storage root at a time).

## More help

- Full behaviour: `USER_GUIDE.md`
- Problems and diagnostics: `TROUBLESHOOTING.md`
- Project overview for developers: `README.md`
