# Third-party notices

QuickAnalyser author work is **MIT**, **Copyright (c) 2026 Soji**, developer signature **by Soji**.
Third-party dependency terms remain separate and are reproduced under `licenses/`.

Inventory date: 25 October 2026. Versions match the pinned `go.mod` / `go.sum` used for this
development tree. Runtime-linked modules were confirmed with:

```text
go list -deps -f "{{if .Module}}{{.Module.Path}} {{.Module.Version}}{{end}}" ./cmd/quickanalyser
```

Before tagging a public release, re-verify each text against the exact module zip in `go.sum`.

## Direct dependencies

| Component | Version | License (upstream) | Role | License text |
|---|---|---|---|---|
| modernc.org/sqlite | v1.34.5 | BSD-3-Clause | CGO-free SQLite driver | `licenses/modernc.org_sqlite_v1.34.5/LICENSE` |

## Transitive modules (go.mod require / linked into Windows amd64 binary)

| Component | Version | License (upstream) | Linked in `./cmd/quickanalyser` | License text |
|---|---|---|---|---|
| modernc.org/libc | v1.55.3 | BSD-3-Clause (+ Go AUTHORS BSD in LICENSE-GO) | yes | `licenses/modernc.org_libc_v1.55.3/` |
| modernc.org/mathutil | v1.6.0 | BSD-3-Clause | yes | `licenses/modernc.org_mathutil_v1.6.0/LICENSE` |
| modernc.org/memory | v1.8.0 | BSD-3-Clause (+ Go AUTHORS / mmap-go notices) | yes | `licenses/modernc.org_memory_v1.8.0/` |
| github.com/dustin/go-humanize | v1.0.1 | MIT | yes | `licenses/github.com_dustin_go-humanize_v1.0.1/LICENSE` |
| github.com/mattn/go-isatty | v0.0.20 | MIT | yes | `licenses/github.com_mattn_go-isatty_v0.0.20/LICENSE` |
| github.com/ncruces/go-strftime | v0.1.9 | MIT | yes | `licenses/github.com_ncruces_go-strftime_v0.1.9/LICENSE` |
| github.com/remyoudompheng/bigfft | v0.0.0-20230129092748-24d4a6f8daec | BSD-3-Clause | yes | `licenses/github.com_remyoudompheng_bigfft_v0.0.0-20230129092748-24d4a6f8daec/LICENSE` |
| github.com/google/uuid | v1.6.0 | BSD-3-Clause | no (module graph / go.mod indirect) | `licenses/github.com_google_uuid_v1.6.0/LICENSE` |
| golang.org/x/sys | v0.22.0 | BSD-3-Clause | no (module graph / go.mod indirect) | `licenses/golang.org_x_sys_v0.22.0/LICENSE` |

Modules present only as test/tooling requirements of upstream packages and **not** listed in
`go.mod` for this application (for example `modernc.org/cc*`, `golang.org/x/tools`) are not
shipped in the QuickAnalyser binary and are omitted from the distribution notice set.

## Go toolchain / standard library

Built and verified locally with the Go toolchain installed on the development machine
(Go 1.27.0 windows/amd64 observed). CI pins Go **1.22.12** to match `go 1.22` in `go.mod`.
The Go standard library / toolchain license text is packaged from the local `GOROOT`:

- Source: `%GOROOT%\LICENSE` (observed `C:\Program Files\Go\LICENSE` on this machine)
- Packaged copy: `licenses/go-standard-library/LICENSE`

## License text verification (26 October 2026)

| Module | Pinned version | Verification |
|---|---|---|
| github.com/google/uuid | v1.6.0 | SHA-256 match vs `GOMODCACHE` module zip LICENSE (`0A8D61ED…4B2D`) |
| golang.org/x/sys | v0.22.0 | SHA-256 match vs `GOMODCACHE` module zip LICENSE (`2D36597F…C067`) |
| Other `licenses/*` entries | as listed above | Previously matched audit cache; retained |

## Status

Pinned-version attribution and upstream license texts under `licenses/` are included for the
modules above. Package portable Windows builds with this file, root `LICENSE`
(MIT, Copyright (c) 2026 Soji, signature “by Soji”), and the `licenses/` tree. This does
**not** by itself authorize a tagged GitHub Release; keep builds labelled
development / unreleased until qualification sign-off.
