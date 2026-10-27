# QuickAnalyser — Troubleshooting and debugging

**by Soji** · MIT License · Copyright (c) 2026 Soji

Use this guide for local problems with the portable Windows package. Prefer the application’s own **Quit**, **Diagnostics**, and cleanup actions over deleting your whole data directory. Do **not** disable antivirus or run as Administrator as a default workaround.

Application diagnostics and support bundles may contain operational detail from your environment (paths, timing, errors). **Review and redact before sharing.**

## Symptom → checks → action

### Browser does not open

1. Confirm `QuickAnalyser.exe` is still running (Task Manager).
2. Read the console window (if started from a terminal) for a line containing `listening on http://127.0.0.1:…`.
3. Open that URL manually in your browser.
4. Relaunch without `--no-browser` if you used that flag.
5. Check popup blockers only for **source tabs** opened from Log Viewer / Highlights — the main UI is a normal page.

### Application already running / wrong or stale executable

1. Only one instance should own a given storage root. A second start may report that another instance owns the storage root.
2. Use **Quit** in the running UI, wait for the process to exit, then start the intended EXE.
3. Confirm you launched the EXE from the folder you extracted (not an older copy elsewhere).
4. Compare **About** `buildTime` / `commit` / `dirty` with `BUILD_INFO.json` in the package. Optionally verify `QuickAnalyser.exe` SHA-256 against `SHA256SUMS.txt`.

### Source code changes not visible in the UI

Embedded UI/assets are baked into the EXE at build time. Editing files under a development tree does **not** update an already-built executable. Rebuild/repackage and run the new EXE (or use an authorized development run path). Refreshing the browser alone cannot load new embedded assets from an old binary.

### Processing appears stalled

1. Check **Processing** stage text and whether **Cancel** is available.
2. Confirm free space on the selected storage root (**Settings** / storage summary).
3. Large archives take longer; watch for progress updates rather than assuming a hang immediately.
4. After cancel or failure, read coverage / error summaries on the job.
5. Open **Diagnostics** → **Refresh status** for local health notes.

### Insufficient or unavailable storage

1. Choose another root under **Settings** (**Use this location**) with enough free space.
2. Do not point the working root at a missing drive or read-only location.
3. Portable mode uses `QuickAnalyserData` beside the executable — ensure that path is writable.

### Missing logs / incomplete coverage / stale saved analysis

1. In **Log Viewer**, clear filename filters (**Find log file**) and scroll the full list.
2. Check coverage notes (partial, truncated, metadata-only, empty).
3. Named non-text `.log` files may be listed but not openable.
4. For older analyses after a classifier update, use **Refresh** on the analysis when offered; if Overview says reanalyse is required, re-upload/reanalyse the original snapshot.
5. Extensionless SGVE-like files under `/logs/` may remain non-log due to a current classification limitation (known limitation).

### Highlights missing after classification refresh

1. Open-path classification can update file kinds without immediately rebuilding Highlights.
2. Use the explicit analysis **Refresh** so pending findings can rebuild.
3. If Refresh fails, prior findings should remain; retry after restoring evidence or free space.
4. If the UI still shows incomplete Highlights, capture Diagnostics timestamps and open a bug report (template below).

### Blank regions while scrolling a large log

1. Wait briefly for labelled **Loading lines …** placeholders to fill.
2. Use **Go to line**, **Start**, or **End** to re-anchor.
3. Expand/collapse **Details** or toggle **Wrap lines**, then scroll again.
4. Confirm you are not viewing a failed/cancelled load (status line under the viewer).
5. Persistent unlabelled blank areas after loading settles are a defect — report with viewport size and approximate line numbers.

### Search failures or incomplete counts

1. Use **Search** on the source tab; wait for **Search complete** or an explicit incomplete/partial status.
2. Matching-line and occurrence totals come from the search result object — not the number of rows on the current page.
3. Use **Previous search page** / **Next search page** when shown.
4. **Clear** returns to browse mode.
5. If the status shows an HTTP or contract error, copy the exact message (without session cookies) into a bug report.

### Corrupt or unsupported archives

1. Confirm the file is `.tar`, `.tar.gz`, or `.tgz`.
2. Re-copy the archive; verify it opens in another tool if needed.
3. Read coverage issues for members that failed.
4. Try a smaller synthetic fixture to separate archive damage from application faults.

### Safe shutdown and restart

1. Click **Quit** in the application.
2. Confirm the process exited.
3. Start `QuickAnalyser.exe` again with the same storage choice.
4. Closing a browser tab alone may **not** stop the engine.

## Diagnostics UI (actual controls)

Under **Diagnostics** (also reachable from Settings diagnostics controls):

| Control | Behaviour |
|---|---|
| **Refresh status** | Reloads local diagnostics health/status |
| **Enable DEBUG (temporary)** | Raises log level to DEBUG for a timed session (default **15 minutes**, same idea as `--debug-minutes 15`) |
| **Disable DEBUG** | Returns to normal INFO logging |
| **Create local support bundle** | Builds a **local** redacted zip under the storage root’s diagnostics bundles area — no automatic upload |

CLI (when starting the EXE from a terminal):

- `--log-level info` (default) or `--log-level debug`
- `--debug-minutes 15` (DEBUG lifetime when debug is enabled via CLI)

### Where logs live

Diagnostics are stored under the **selected working storage root**, in a `diagnostics` directory (rotated JSONL logs plus a `bundles` subdirectory for support zips). Exact filenames rotate; use **Refresh status** and **Create local support bundle** rather than guessing paths from memory.

These are **built-in defaults** in the current implementation (not Settings controls):

| Limit | Default |
|---|---|
| Rotated JSONL files | **5** |
| Size per JSONL file | **10 MiB** |
| Aggregate JSONL limit | **50 MiB** across those files |
| Maximum event size | **8 KiB** (larger fields are truncated) |
| Temporary DEBUG duration | **15 minutes** (UI **Enable DEBUG (temporary)** or CLI `--debug-minutes 15`) |

Older JSONL files are pruned when rotation/retention limits are hit. There is no UI to change these caps.

**Support-bundle retention** is separate: the application keeps up to **5** local support-bundle zips under `diagnostics/bundles/` (oldest pruned when creating a new one). Bundles are redacted for export safety but can still contain operational detail — **review before sharing**.

### Identify the build

1. Open **About** in the UI for product, version, signature, `buildTime`, `commit`, `dirty`, and status.
2. Or read `BUILD_INFO.json` next to the EXE.
3. Compute SHA-256 of `QuickAnalyser.exe` and compare to `SHA256SUMS.txt` in the package.

Never paste session tokens (`s=` query values or `X-QA-Session` headers) into bug reports.

## Bug-report template (sanitized)

```text
QuickAnalyser bug report
------------------------
Version / About buildTime / commit / dirty / status:
EXE SHA-256 (from SHA256SUMS or Get-FileHash):
Package name (ZIP), if applicable:

Windows version / architecture (e.g. Windows 10/11 amd64):

Steps to reproduce:
1.
2.
3.

Expected result:
Actual result:

Archive type/size (no customer content): e.g. .tar.gz ~12 MB synthetic / redacted
Coverage summary (complete/partial/truncated; counts if shown):

Relevant sanitized diagnostic timestamps / error codes (no secrets, no session tokens):

Reproduces with synthetic fixture? yes/no (which fixture if yes):

Screenshots: attach only redacted UI captures
```

## What not to do

- Do not delete the entire working-data directory as a first step.
- Do not share raw customer archives, license keys, or unreviewed diagnostics bundles.
- Do not treat Highlights detections as confirmed incidents without review.
