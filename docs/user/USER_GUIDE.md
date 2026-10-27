# QuickAnalyser — User Guide

**by Soji** · MIT License · Copyright (c) 2026 Soji  
Version label: see `VERSION` / About in the running app.

This guide describes **implemented** behaviour of the local QuickAnalyser UI and portable package. It does not invent settings or promise unsupported platforms.

Suspicious log matches shown under **Log Highlights** are **detection results** (pattern-based findings with evidence lines). They are not confirmed security incidents or root-cause conclusions.

## 1. Executable vs working data

| Concept | Meaning |
|---|---|
| Executable | `QuickAnalyser.exe` (and package files beside it) |
| Working storage root | Where uploads, temporary files, evidence, SQLite results, searches, exports, and diagnostics live |

Choose the storage root at first run or under **Settings**. Modes:

- Default user-data location
- Choose a working directory
- **Portable: QuickAnalyserData beside the executable**

Confirm with **Use this location**. Free space is shown when available. Switching roots does not silently move or delete another root’s data; follow on-screen prompts. Prefer local disks with adequate free space.

CLI (optional): `--data-dir <absolute-path>`, `--portable`, `--no-browser`.

## 2. Snapshots and processing

**Upload Snapshot** accepts `.tar`, `.tar.gz`, and `.tgz`. Nested archives and gzip-compressed text members are processed with bounded limits (expanded bytes, recursion, lines/events, storage). One analysis is active for processing at a time; you may **view** a different saved analysis while another finishes only as the UI allows.

Progress appears under **Processing**. Use **Cancel** when offered to stop the active job. Coverage issues (skipped, corrupt, truncated, or limited sources) remain visible in results.

## 3. Isolation of views

- **Upload / Processing** concerns the active upload job.
- **Previous Analyses** opens a saved job for viewing (**Overview**, **Log Highlights**, **Log Viewer**, **Configuration**, **Processing Details**).
- Switching the viewed analysis should not mix another job’s Highlights or file list into the current view. Background progress refers to the active processing job when one is running.

## 4. Overview

**Overview** shows curated profile attributes when evidence supports them: field values, confidence/status, and source references (path and line ranges when available). Distinctions such as installed/current vs historical or component-scoped values are preserved when the profile provides them.

Unknown or unavailable attributes are omitted or marked honestly — the tool does not invent health scores or root causes. Unknown products still get generic analysis.

## 5. Log Highlights

**Log Highlights** lists detection results grouped under file headers:

- Actual suspicious lines / snippets with severity
- Grouping of repeated occurrences
- Categories / rule identifiers as shown in the UI
- Date reference and sorting / range controls in the workspace header (for example Sort, Range, undated) when present

Use **Open** / line-range links to open evidence in a source tab. Treat matches as detections requiring human judgment.

## 6. Log Viewer

**Log Viewer** lists **logs** (not mixed configuration/scripts as logs). Header control **Find log file** filters the inventory by filename/path.

Named `.log` files and common rotations are listed even under configuration-like paths. Statuses you may see:

- Readable text logs (openable)
- Empty logs
- Non-text / blocked named logs listed as metadata-only and not openable as text

Open a log to a **separate browser tab** (popup-blocker fallback may apply).

### Source tab (continuous viewer)

In the source tab you can:

- Scroll continuously through retained lines (sections load on demand)
- See labelled **Loading lines …** placeholders for demanded unloaded ranges
- Use **Start**, **Earlier**, **Go to line** / **Go**, **Later**, **End**
- Toggle **Wrap lines**, **Case sensitive**, **Matching lines only** (for search results)
- Search the **entire retained source** with **Search** / **Clear**
- Read matching-line and occurrence counts with explicit final/incomplete status — page length is not the full-file total

Technical section/cache counters appear under **Details** when expanded.

## 7. Configuration tab

**Configuration** lists configuration-oriented files separately from logs (for example **Find configuration file**). Severity words alone do not force a file to be treated as a log.

## 8. Saved history, refresh, and reanalysis

**Previous Analyses** lists saved jobs under the current storage root.

- **Open** — view retained results; classification may refresh kinds without always rebuilding Highlights immediately.
- If Highlights look incomplete after a classifier update, use the analysis **Refresh** action (when shown) so findings can rebuild. Pending rebuild state is retained across open/restart until a successful rebuild clears it. Failed rebuilds keep prior findings when possible.
- Some older analyses may require a full **reanalyse** of the original snapshot (for example missing profile/release evidence). Messages under compatibility/Overview explain when.
- Identical archive content may reuse prior work when compatibility checks pass.

### Cleanup vs delete

| Action | Effect |
|---|---|
| **Clean temp/search** | Removes disposable temporary, search, and export data for that analysis. Retained upload copy, evidence, and findings stay so you can reopen results. |
| **Delete analysis** | Permanently removes that saved analysis (confirm in the UI). Not a routine fix for viewer issues. |

Do **not** delete your entire data directory as a normal troubleshooting step.

## 9. Exports and privacy

Export actions produce local JSON (or similar) under the job’s export area. Exports can contain log snippets and attribute values from the snapshot — **review before sharing**. There is no automatic cloud upload or telemetry in the default product.

## 10. Diagnostics

**Diagnostics** (and Settings diagnostics controls) cover local operational logs under the selected storage root (`diagnostics/` relative to that root). See `TROUBLESHOOTING.md` for DEBUG enable/disable, support bundles, and bug reports.

## 11. Known limitations

- **SGVE-like extensionless** key=value files under `/logs/` (for example names without `.log`) may remain non-log with the current classifier. Named `.log` files are listed.
- Native **Linux** support is separate; this guide’s package focus is Windows amd64.
- **Clean Windows laptop/VM** verification and remote CI are release gates tracked separately — not assumed here.
- Development packages may report `commit=unknown` / dirty in About while still usable locally; that is not a stable published release.

## 12. Branding and license

QuickAnalyser · **by Soji** · MIT · Copyright (c) 2026 Soji. See `LICENSE` and `THIRD_PARTY_NOTICES.md`.
