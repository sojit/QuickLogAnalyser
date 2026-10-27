# Temporary-repository cases for build metadata / release refusal.
# Does not modify the development checkout Git state.
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

. (Join-Path $PSScriptRoot 'lib\Get-QABuildMetadata.ps1')

$failures = New-Object System.Collections.Generic.List[string]
function Expect-True {
  param([bool]$Cond, [string]$Msg)
  if (-not $Cond) { $script:failures.Add($Msg); Write-Host "FAIL: $Msg" } else { Write-Host "PASS: $Msg" }
}

$scratch = Join-Path $env:TEMP ("qa-meta-" + [guid]::NewGuid().ToString('n'))
New-Item -ItemType Directory -Force -Path $scratch | Out-Null
Write-Host "Scratch=$scratch"

function Invoke-GitIn {
  param([string]$Dir, [Parameter(Mandatory = $true)][string[]]$GitArgs)
  Push-Location $Dir
  $prev = $ErrorActionPreference
  $ErrorActionPreference = 'Continue'
  try {
    & git @GitArgs 2>&1 | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "git $($GitArgs -join ' ') failed ($LASTEXITCODE) in $Dir" }
  }
  finally {
    $ErrorActionPreference = $prev
    Pop-Location
  }
}

try {
  # --- Case A: missing Git worktree (plain directory) ---
  $noGit = Join-Path $scratch 'no-git'
  New-Item -ItemType Directory -Force -Path $noGit | Out-Null
  Set-Content -LiteralPath (Join-Path $noGit 'VERSION') -Value "QA-v1.0-2026`n"
  New-Item -ItemType Directory -Force -Path (Join-Path $noGit 'internal\appinfo') | Out-Null
  Set-Content -LiteralPath (Join-Path $noGit 'internal\appinfo\version.txt') -Value 'QA-v1.0-2026' -NoNewline
  $m = Get-QABuildMetadata -RepoRoot $noGit -Mode development
  Expect-True ($m.Commit -eq 'unknown') 'missing-Git: commit unknown'
  Expect-True ($m.Dirty -eq 'true') 'missing-Git: dirty true'
  Expect-True (-not $m.HasHEAD) 'missing-Git: no HEAD'
  $refused = $false
  try { Assert-QAReleaseMetadata -Metadata $m -RepoRoot $noGit -Version 'QA-v1.0-2026' } catch { $refused = $true }
  Expect-True $refused 'missing-Git: release refused'

  # --- Case B: unborn repository (git init, no commit) ---
  $unborn = Join-Path $scratch 'unborn'
  New-Item -ItemType Directory -Force -Path $unborn | Out-Null
  Set-Content -LiteralPath (Join-Path $unborn 'VERSION') -Value "QA-v1.0-2026`n"
  New-Item -ItemType Directory -Force -Path (Join-Path $unborn 'internal\appinfo') | Out-Null
  Set-Content -LiteralPath (Join-Path $unborn 'internal\appinfo\version.txt') -Value 'QA-v1.0-2026' -NoNewline
  Invoke-GitIn $unborn @('init', '-b', 'main')
  Invoke-GitIn $unborn @('config', 'user.email', 'qa-meta-test@example.com')
  Invoke-GitIn $unborn @('config', 'user.name', 'QA Meta Test')
  $m = Get-QABuildMetadata -RepoRoot $unborn -Mode development
  Expect-True ($m.GitAvailable -eq $true) 'unborn: git available'
  Expect-True (-not $m.HasHEAD -or $m.Commit -eq 'unknown') 'unborn: commit unknown / no HEAD'
  Expect-True ($m.Dirty -eq 'true') 'unborn: dirty true'
  $refused = $false
  try { Assert-QAReleaseMetadata -Metadata $m -RepoRoot $unborn -Version 'QA-v1.0-2026' } catch { $refused = $true }
  Expect-True $refused 'unborn: release refused'

  # --- Case C: clean committed tree ---
  $clean = Join-Path $scratch 'clean'
  New-Item -ItemType Directory -Force -Path $clean | Out-Null
  Set-Content -LiteralPath (Join-Path $clean 'VERSION') -Value "QA-v1.0-2026`n"
  New-Item -ItemType Directory -Force -Path (Join-Path $clean 'internal\appinfo') | Out-Null
  Set-Content -LiteralPath (Join-Path $clean 'internal\appinfo\version.txt') -Value 'QA-v1.0-2026' -NoNewline
  Set-Content -LiteralPath (Join-Path $clean 'README.md') -Value "fixture`n"
  Invoke-GitIn $clean @('init', '-b', 'main')
  Invoke-GitIn $clean @('config', 'user.email', 'qa-meta-test@example.com')
  Invoke-GitIn $clean @('config', 'user.name', 'QA Meta Test')
  Invoke-GitIn $clean @('add', 'VERSION', 'README.md', 'internal/appinfo/version.txt')
  Invoke-GitIn $clean @('commit', '-m', 'clean fixture')
  $m = Get-QABuildMetadata -RepoRoot $clean -Mode development
  Expect-True ($m.HasHEAD -eq $true) 'clean: has HEAD'
  Expect-True ($m.Commit -ne 'unknown') 'clean: commit known'
  Expect-True ($m.Dirty -eq 'false') 'clean: dirty false'
  Expect-True ($m.DirtyReason -eq 'clean') 'clean: reason clean'
  Expect-True ($m.BuildTime -match '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$') 'clean: UTC build time format'
  $ok = $true
  try { Assert-QAReleaseMetadata -Metadata $m -RepoRoot $clean -Version 'QA-v1.0-2026' } catch { $ok = $false; Write-Host $_ }
  Expect-True $ok 'clean: release accepted'

  # --- Case D: modified tracked file ---
  $modified = Join-Path $scratch 'modified'
  Copy-Item -Recurse -Force $clean $modified
  # Remap .git if copy broke it — recreate from clean commit by re-init is safer
  Remove-Item -Recurse -Force $modified -ErrorAction SilentlyContinue
  New-Item -ItemType Directory -Force -Path $modified | Out-Null
  Set-Content -LiteralPath (Join-Path $modified 'VERSION') -Value "QA-v1.0-2026`n"
  New-Item -ItemType Directory -Force -Path (Join-Path $modified 'internal\appinfo') | Out-Null
  Set-Content -LiteralPath (Join-Path $modified 'internal\appinfo\version.txt') -Value 'QA-v1.0-2026' -NoNewline
  Set-Content -LiteralPath (Join-Path $modified 'README.md') -Value "fixture`n"
  Invoke-GitIn $modified @('init', '-b', 'main')
  Invoke-GitIn $modified @('config', 'user.email', 'qa-meta-test@example.com')
  Invoke-GitIn $modified @('config', 'user.name', 'QA Meta Test')
  Invoke-GitIn $modified @('add', 'VERSION', 'README.md', 'internal/appinfo/version.txt')
  Invoke-GitIn $modified @('commit', '-m', 'base')
  Set-Content -LiteralPath (Join-Path $modified 'README.md') -Value "modified`n"
  $m = Get-QABuildMetadata -RepoRoot $modified -Mode development
  Expect-True ($m.Dirty -eq 'true') 'modified: dirty true'
  Expect-True ($m.DirtyReason -eq 'working-tree-dirty') 'modified: dirty reason'
  $refused = $false
  try { Assert-QAReleaseMetadata -Metadata $m -RepoRoot $modified -Version 'QA-v1.0-2026' } catch { $refused = $true }
  Expect-True $refused 'modified: release refused'

  # --- Case E: untracked source file ---
  $untracked = Join-Path $scratch 'untracked'
  New-Item -ItemType Directory -Force -Path $untracked | Out-Null
  Set-Content -LiteralPath (Join-Path $untracked 'VERSION') -Value "QA-v1.0-2026`n"
  New-Item -ItemType Directory -Force -Path (Join-Path $untracked 'internal\appinfo') | Out-Null
  Set-Content -LiteralPath (Join-Path $untracked 'internal\appinfo\version.txt') -Value 'QA-v1.0-2026' -NoNewline
  Set-Content -LiteralPath (Join-Path $untracked 'README.md') -Value "fixture`n"
  Invoke-GitIn $untracked @('init', '-b', 'main')
  Invoke-GitIn $untracked @('config', 'user.email', 'qa-meta-test@example.com')
  Invoke-GitIn $untracked @('config', 'user.name', 'QA Meta Test')
  Invoke-GitIn $untracked @('add', 'VERSION', 'README.md', 'internal/appinfo/version.txt')
  Invoke-GitIn $untracked @('commit', '-m', 'base')
  Set-Content -LiteralPath (Join-Path $untracked 'extra.go') -Value "package main`n"
  $m = Get-QABuildMetadata -RepoRoot $untracked -Mode development
  Expect-True ($m.Dirty -eq 'true') 'untracked: dirty true'
  $refused = $false
  try { Assert-QAReleaseMetadata -Metadata $m -RepoRoot $untracked -Version 'QA-v1.0-2026' } catch { $refused = $true }
  Expect-True $refused 'untracked: release refused'

  # --- Case F: VERSION mismatch vs embedded ---
  $mismatch = Join-Path $scratch 'mismatch'
  New-Item -ItemType Directory -Force -Path $mismatch | Out-Null
  Set-Content -LiteralPath (Join-Path $mismatch 'VERSION') -Value "QA-v1.0-2026`n"
  New-Item -ItemType Directory -Force -Path (Join-Path $mismatch 'internal\appinfo') | Out-Null
  Set-Content -LiteralPath (Join-Path $mismatch 'internal\appinfo\version.txt') -Value 'WRONG' -NoNewline
  Set-Content -LiteralPath (Join-Path $mismatch 'README.md') -Value "fixture`n"
  Invoke-GitIn $mismatch @('init', '-b', 'main')
  Invoke-GitIn $mismatch @('config', 'user.email', 'qa-meta-test@example.com')
  Invoke-GitIn $mismatch @('config', 'user.name', 'QA Meta Test')
  Invoke-GitIn $mismatch @('add', 'VERSION', 'README.md', 'internal/appinfo/version.txt')
  Invoke-GitIn $mismatch @('commit', '-m', 'mismatch')
  $m = Get-QABuildMetadata -RepoRoot $mismatch -Mode development
  Expect-True ($m.Dirty -eq 'false') 'mismatch: tree clean'
  $refused = $false
  try { Assert-QAReleaseMetadata -Metadata $m -RepoRoot $mismatch -Version 'QA-v1.0-2026' } catch { $refused = $true }
  Expect-True $refused 'mismatch: release refused on VERSION inconsistency'
}
finally {
  Remove-Item -Recurse -Force $scratch -ErrorAction SilentlyContinue
}

# JSON identity round-trip must keep RFC3339 build_time (no locale spaces) for ldflags.
{
  $id = [pscustomobject]@{
    version = 'QA-v1.0-2026'; commit = 'abcdef1'; dirty = 'false'
    build_time = '2026-10-27T16:47:18Z'; mode = 'development'; status = 'development_unreleased'
    os = 'windows'; arch = 'amd64'
  }
  $path = Join-Path $env:TEMP ('qa-id-' + [guid]::NewGuid().ToString('N') + '.json')
  try {
    Save-QABuildIdentity -Identity $id -Path $path
    $loaded = ConvertTo-QABuildIdentity -Path $path
    Expect-True ($loaded.build_time -match '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$') "round-trip build_time RFC3339 got $($loaded.build_time)"
    $ld = Get-QALdFlagsFromIdentity -Identity $loaded
    Expect-True ($ld -notmatch 'BuildTime=\S*\s') "ldflags BuildTime must not contain whitespace: $ld"
    Expect-True ($ld -match 'BuildTime=2026-10-27T16:47:18Z') "ldflags contain normalized BuildTime"
  }
  finally {
    Remove-Item -Force -LiteralPath $path -ErrorAction SilentlyContinue
  }
}

# Package basename must not embed commit "unknown".
{
  $devId = [pscustomobject]@{ version = 'QA-v1.0-2026'; os = 'windows'; arch = 'amd64'; commit = 'unknown'; dirty = 'true' }
  $devName = Get-QAPackageBaseName -Identity $devId
  Expect-True ($devName -eq 'QA-v1.0-2026-windows-amd64-dev') "dev package name got $devName"
  Expect-True ($devName -notmatch 'unknown') 'dev package name must not contain unknown'

  $relId = [pscustomobject]@{ version = 'QA-v1.0-2026'; os = 'windows'; arch = 'amd64'; commit = 'abcdef1'; dirty = 'false' }
  $relName = Get-QAPackageBaseName -Identity $relId -Release
  Expect-True ($relName -eq 'QA-v1.0-2026-windows-amd64') "release package name got $relName"

  $dirtyRel = Get-QAPackageBaseName -Identity $devId -Release
  Expect-True ($dirtyRel -eq 'QA-v1.0-2026-windows-amd64-dev') 'dirty/unknown still forces -dev even with -Release switch on helper'
}

if ($failures.Count -gt 0) {
  Write-Host "`n$($failures.Count) failure(s)"
  exit 1
}
Write-Host "`ntest_build_metadata: ALL PASS"
exit 0
