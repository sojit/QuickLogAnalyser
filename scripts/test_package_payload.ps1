# Validate portable package Markdown links and (when present) ZIP payload integrity.
# Prefer verifying an existing dist ZIP; optionally assemble a docs-only stage for link checks.
param(
  [string]$RepoRoot = '',
  [string]$ZipPath = '',
  [switch]$SkipZipIfMissing
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

. (Join-Path $PSScriptRoot 'lib\Get-QABuildMetadata.ps1')
. (Join-Path $PSScriptRoot 'lib\Test-QAMarkdownLinks.ps1')

if ([string]::IsNullOrWhiteSpace($RepoRoot)) {
  $RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
}

$failures = New-Object System.Collections.Generic.List[string]
function Expect-True([bool]$Cond, [string]$Msg) {
  if (-not $Cond) { [void]$failures.Add($Msg); Write-Host "FAIL: $Msg" }
  else { Write-Host "PASS: $Msg" }
}

# --- Repository README must keep docs/user/ guide links ---
$repoReadme = Join-Path $RepoRoot 'README.md'
$repoLinkResult = Test-QAMarkdownLinksInRoot -Root $RepoRoot -Files @('README.md')
Expect-True ($repoLinkResult.Failures.Count -eq 0) "repo README local links ($($repoLinkResult.Failures.Count) failures)"
foreach ($f in $repoLinkResult.Failures) { Write-Host "  $f" }
$repoText = Get-Content -LiteralPath $repoReadme -Raw
Expect-True ($repoText -match 'docs/user/QUICK_START\.md') 'repo README links docs/user/QUICK_START.md'
Expect-True ($repoText -match 'docs/user/USER_GUIDE\.md') 'repo README links docs/user/USER_GUIDE.md'
Expect-True ($repoText -match 'docs/user/TROUBLESHOOTING\.md') 'repo README links docs/user/TROUBLESHOOTING.md'

# --- Maintained package README source uses root-relative guide links ---
$pkgReadmeSrc = Join-Path $RepoRoot 'docs\user\PACKAGE_README.md'
Expect-True (Test-Path -LiteralPath $pkgReadmeSrc) 'docs/user/PACKAGE_README.md exists'
$pkgSrcRoot = Join-Path $RepoRoot 'docs\user'
$pkgSrcLinks = Test-QAMarkdownLinksInRoot -Root $pkgSrcRoot -Files @('PACKAGE_README.md') `
  -RequiredFromReadme @('QUICK_START.md', 'USER_GUIDE.md', 'TROUBLESHOOTING.md')
Expect-True ($pkgSrcLinks.Failures.Count -eq 0) "PACKAGE_README.md local links ($($pkgSrcLinks.Failures.Count) failures)"
foreach ($f in $pkgSrcLinks.Failures) { Write-Host "  $f" }

# --- Synthetic stage: same layout as packaging (no EXE required for link test) ---
$stage = Join-Path $env:TEMP ('qa-pkg-links-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $stage | Out-Null
try {
  Copy-Item (Join-Path $RepoRoot 'docs\user\PACKAGE_README.md') (Join-Path $stage 'README.md')
  foreach ($g in @('QUICK_START.md', 'USER_GUIDE.md', 'TROUBLESHOOTING.md')) {
    Copy-Item (Join-Path $RepoRoot "docs\user\$g") (Join-Path $stage $g)
  }
  $stageLinks = Test-QAMarkdownLinksInRoot -Root $stage -Files @('README.md', 'QUICK_START.md', 'USER_GUIDE.md', 'TROUBLESHOOTING.md') `
    -RequiredFromReadme @('QUICK_START.md', 'USER_GUIDE.md', 'TROUBLESHOOTING.md')
  Expect-True ($stageLinks.Failures.Count -eq 0) "synthetic package stage Markdown links ($($stageLinks.Failures.Count) failures)"
  foreach ($f in $stageLinks.Failures) { Write-Host "  $f" }
  Expect-True ($stageLinks.ResolvedGuides.Count -ge 3) 'synthetic stage README resolves all three guides'
}
finally {
  Remove-Item -Recurse -Force -LiteralPath $stage -ErrorAction SilentlyContinue
}

# --- Actual ZIP payload (when present) ---
if ([string]::IsNullOrWhiteSpace($ZipPath)) {
  $ZipPath = Join-Path $RepoRoot 'dist\QA-v1.0-2026-windows-amd64-dev.zip'
}
if (-not (Test-Path -LiteralPath $ZipPath)) {
  if ($SkipZipIfMissing) {
    Write-Host "SKIP: ZIP not found at $ZipPath"
  } else {
    Expect-True $false "development ZIP missing: $ZipPath (run scripts/package_windows.ps1 first, or pass -SkipZipIfMissing)"
  }
} else {
  Expect-True ($ZipPath -notmatch 'unknown') 'ZIP path must not contain unknown'
  $extract = Join-Path $env:TEMP ('qa-pkg-extract-' + [guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory -Force -Path $extract | Out-Null
  try {
    Expand-Archive -LiteralPath $ZipPath -DestinationPath $extract -Force
    $required = @(
      'QuickAnalyser.exe', 'QUICK_START.md', 'USER_GUIDE.md', 'TROUBLESHOOTING.md',
      'README.md', 'VERSION', 'BUILD_INFO.json', 'LICENSE', 'THIRD_PARTY_NOTICES.md',
      'SHA256SUMS.txt', 'licenses'
    )
    foreach ($r in $required) {
      Expect-True (Test-Path -LiteralPath (Join-Path $extract $r)) "payload present: $r"
    }
    # Forbidden runtime/customer material (extension check; avoid Get-ChildItem -Include quirks)
    $forbiddenExt = @('.sqlite', '.sqlite-wal', '.sqlite-shm', '.log', '.tgz', '.tar', '.gz', '.db', '.wal')
    $bad = @(Get-ChildItem -LiteralPath $extract -Recurse -File | Where-Object {
      $n = $_.Name
      $ext = $_.Extension
      if ($forbiddenExt -contains $ext.ToLowerInvariant()) { return $true }
      if ($n -match '(?i)\.(sqlite(-wal|-shm)?|tar\.gz)$') { return $true }
      if ($n -match '(?i)^(results|userdata|diagnostics)') { return $true }
      return $false
    })
    Expect-True ($bad.Count -eq 0) "no runtime/customer archives or DBs in payload (found $($bad.Count))"
    if ($bad.Count -gt 0) { $bad | ForEach-Object { Write-Host ("  forbidden: " + $_.FullName) } }

    $sumsPath = Join-Path $extract 'SHA256SUMS.txt'
    $sumFails = 0
    foreach ($line in (Get-Content -LiteralPath $sumsPath)) {
      if ([string]::IsNullOrWhiteSpace($line)) { continue }
      $parts = if ($line -match '^(\S+)\s{2,}(.+)$') { @($Matches[1], $Matches[2]) } else { ($line -split '\s+', 2) }
      $expect = $parts[0].Trim(); $rel = $parts[1].Trim().Replace('/', '\')
      $path = Join-Path $extract $rel
      if (-not (Test-Path -LiteralPath $path)) { $sumFails++; Write-Host "FAIL checksum missing: $rel"; continue }
      $got = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash
      if ($got -ne $expect) { $sumFails++; Write-Host "FAIL checksum mismatch: $rel" }
    }
    Expect-True ($sumFails -eq 0) "extracted SHA256SUMS ($sumFails failures)"

    $sidecar = $ZipPath + '.sha256'
    Expect-True (Test-Path -LiteralPath $sidecar) 'ZIP .sha256 sidecar present'
    $sideText = (Get-Content -Raw -LiteralPath $sidecar).Trim()
    $zipHash = (Get-FileHash -LiteralPath $ZipPath -Algorithm SHA256).Hash
    Expect-True ($sideText.StartsWith($zipHash, [StringComparison]::OrdinalIgnoreCase)) 'sidecar SHA-256 matches ZIP'

    $info = Get-Content -Raw (Join-Path $extract 'BUILD_INFO.json') | ConvertFrom-Json
    $exeBytes = [IO.File]::ReadAllBytes((Join-Path $extract 'QuickAnalyser.exe'))
    $exeText = [Text.Encoding]::ASCII.GetString($exeBytes)
    Expect-True ($exeText.Contains([string]$info.version)) "EXE embeds BUILD_INFO.version=$($info.version)"
    Expect-True ($exeText.Contains([string]$info.build_time)) "EXE embeds BUILD_INFO.build_time=$($info.build_time)"
    Expect-True ($info.status -eq 'development_unreleased') 'BUILD_INFO.status is development_unreleased'
    Expect-True ($info.status -ne 'released') 'BUILD_INFO.status must not be released for this candidate'

    $zipLinks = Test-QAMarkdownLinksInRoot -Root $extract -Files @('README.md', 'QUICK_START.md', 'USER_GUIDE.md', 'TROUBLESHOOTING.md') `
      -RequiredFromReadme @('QUICK_START.md', 'USER_GUIDE.md', 'TROUBLESHOOTING.md')
    Expect-True ($zipLinks.Failures.Count -eq 0) "extracted ZIP Markdown links ($($zipLinks.Failures.Count) failures)"
    foreach ($f in $zipLinks.Failures) { Write-Host "  $f" }
    Expect-True ($zipLinks.ResolvedGuides.Count -ge 3) 'packaged README resolves QUICK_START, USER_GUIDE, TROUBLESHOOTING'

    # Packaged README must not use docs/user/ as Markdown link targets (prose may mention the source path).
    $packagedReadme = Get-Content -Raw (Join-Path $extract 'README.md')
    $badLinkTargets = [regex]::Matches($packagedReadme, '\[[^\]]*\]\(([^)]+)\)') | ForEach-Object {
      $_.Groups[1].Value
    } | Where-Object { $_ -match 'docs/user/' }
    Expect-True (@($badLinkTargets).Count -eq 0) 'packaged README has no docs/user/ Markdown link targets'
    Expect-True ($packagedReadme -match '\]\(QUICK_START\.md\)') 'packaged README links QUICK_START.md at root'
    Expect-True ($packagedReadme -match '\]\(USER_GUIDE\.md\)') 'packaged README links USER_GUIDE.md at root'
    Expect-True ($packagedReadme -match '\]\(TROUBLESHOOTING\.md\)') 'packaged README links TROUBLESHOOTING.md at root'
  }
  finally {
    Remove-Item -Recurse -Force -LiteralPath $extract -ErrorAction SilentlyContinue
  }
}

if ($failures.Count -gt 0) {
  Write-Host "`n$($failures.Count) failure(s)"
  exit 1
}
Write-Host "`ntest_package_payload: ALL PASS"
exit 0
