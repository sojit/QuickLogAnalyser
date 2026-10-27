# Entry-point ordering regressions for release mode.
# Exercises scripts/build.ps1 -Release -PreflightOnly against isolated temp repos.
# Does not modify the development checkout Git configuration or history.
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$devRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$buildPs1 = Join-Path $devRoot 'scripts\build.ps1'
$failures = New-Object System.Collections.Generic.List[string]
function Expect-True {
  param([bool]$Cond, [string]$Msg)
  if (-not $Cond) { $script:failures.Add($Msg); Write-Host "FAIL: $Msg" } else { Write-Host "PASS: $Msg" }
}

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

function New-FixtureRepo {
  param([string]$Name, [string]$EmbedVersion = 'QA-v1.0-2026', [switch]$Commit)
  $dir = Join-Path $scratch $Name
  New-Item -ItemType Directory -Force -Path (Join-Path $dir 'internal\appinfo') | Out-Null
  $utf8 = New-Object System.Text.UTF8Encoding $false
  [System.IO.File]::WriteAllText((Join-Path $dir 'VERSION'), "QA-v1.0-2026`n", $utf8)
  [System.IO.File]::WriteAllText((Join-Path $dir 'internal\appinfo\version.txt'), $EmbedVersion, $utf8)
  [System.IO.File]::WriteAllText((Join-Path $dir 'README.md'), "fixture`n", $utf8)
  if ($Commit) {
    Invoke-GitIn $dir @('init', '-b', 'main')
    Invoke-GitIn $dir @('config', 'user.email', 'qa-entrypoint-test@example.com')
    Invoke-GitIn $dir @('config', 'user.name', 'QA Entrypoint Test')
    Invoke-GitIn $dir @('add', 'VERSION', 'README.md', 'internal/appinfo/version.txt')
    Invoke-GitIn $dir @('commit', '-m', 'fixture')
  }
  return $dir
}

function Invoke-ReleasePreflight {
  param([string]$RepoRoot)
  $prev = $ErrorActionPreference
  $ErrorActionPreference = 'Continue'
  try {
    $out = & powershell -NoProfile -ExecutionPolicy Bypass -File $buildPs1 -Release -PreflightOnly -RepoRoot $RepoRoot 2>&1
    return [pscustomobject]@{
      ExitCode = $LASTEXITCODE
      Output   = @($out | ForEach-Object { "$_" }) -join "`n"
    }
  }
  finally {
    $ErrorActionPreference = $prev
  }
}

$scratch = Join-Path $env:TEMP ("qa-entry-" + [guid]::NewGuid().ToString('n'))
New-Item -ItemType Directory -Force -Path $scratch | Out-Null
Write-Host "Scratch=$scratch"

try {
  # Clean matching versions → accepted
  $clean = New-FixtureRepo -Name 'clean' -Commit
  $r = Invoke-ReleasePreflight -RepoRoot $clean
  Expect-True ($r.ExitCode -eq 0) 'entrypoint clean matching: accepted'
  Expect-True ((Get-Content (Join-Path $clean 'internal\appinfo\version.txt') -Raw).Trim() -eq 'QA-v1.0-2026') 'entrypoint clean: embed unchanged'

  # Clean committed VERSION mismatch → refused without mutation
  $mismatch = New-FixtureRepo -Name 'mismatch' -EmbedVersion 'WRONG-LABEL' -Commit
  $before = Get-Content (Join-Path $mismatch 'internal\appinfo\version.txt') -Raw
  $r = Invoke-ReleasePreflight -RepoRoot $mismatch
  Expect-True ($r.ExitCode -ne 0) 'entrypoint committed mismatch: refused'
  $after = Get-Content (Join-Path $mismatch 'internal\appinfo\version.txt') -Raw
  Expect-True ($after -eq $before) 'entrypoint committed mismatch: embed preserved (no sync)'
  Expect-True ($after.Trim() -eq 'WRONG-LABEL') 'entrypoint committed mismatch: still WRONG-LABEL'

  # Uncommitted embedded-version edit → refused and edit preserved (the audit reproduction)
  $dirtyEmbed = New-FixtureRepo -Name 'dirty-embed' -Commit
  $utf8 = New-Object System.Text.UTF8Encoding $false
  [System.IO.File]::WriteAllText((Join-Path $dirtyEmbed 'internal\appinfo\version.txt'), 'UNCOMMITTED_EDIT', $utf8)
  $r = Invoke-ReleasePreflight -RepoRoot $dirtyEmbed
  Expect-True ($r.ExitCode -ne 0) 'entrypoint uncommitted embed edit: refused'
  $preserved = (Get-Content (Join-Path $dirtyEmbed 'internal\appinfo\version.txt') -Raw).Trim()
  Expect-True ($preserved -eq 'UNCOMMITTED_EDIT') 'entrypoint uncommitted embed edit: preserved (not overwritten)'

  # Modified tracked source → refused
  $modified = New-FixtureRepo -Name 'modified' -Commit
  Set-Content -LiteralPath (Join-Path $modified 'README.md') -Value "changed`n"
  $r = Invoke-ReleasePreflight -RepoRoot $modified
  Expect-True ($r.ExitCode -ne 0) 'entrypoint modified tracked: refused'

  # Untracked source → refused
  $untracked = New-FixtureRepo -Name 'untracked' -Commit
  Set-Content -LiteralPath (Join-Path $untracked 'extra.go') -Value "package main`n"
  $r = Invoke-ReleasePreflight -RepoRoot $untracked
  Expect-True ($r.ExitCode -ne 0) 'entrypoint untracked source: refused'

  # Unborn repository → refused
  $unborn = New-FixtureRepo -Name 'unborn'
  Invoke-GitIn $unborn @('init', '-b', 'main')
  Invoke-GitIn $unborn @('config', 'user.email', 'qa-entrypoint-test@example.com')
  Invoke-GitIn $unborn @('config', 'user.name', 'QA Entrypoint Test')
  $r = Invoke-ReleasePreflight -RepoRoot $unborn
  Expect-True ($r.ExitCode -ne 0) 'entrypoint unborn: refused'

  # Missing Git → refused
  $noGit = New-FixtureRepo -Name 'no-git'
  $r = Invoke-ReleasePreflight -RepoRoot $noGit
  Expect-True ($r.ExitCode -ne 0) 'entrypoint missing-Git: refused'
}
finally {
  Remove-Item -Recurse -Force $scratch -ErrorAction SilentlyContinue
}

if ($failures.Count -gt 0) {
  Write-Host "`n$($failures.Count) failure(s)"
  exit 1
}
Write-Host "`ntest_release_entrypoint: ALL PASS"
exit 0
