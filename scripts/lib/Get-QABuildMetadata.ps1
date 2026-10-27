# Shared QuickAnalyser build metadata (Windows).
# Dot-source from build/verify/package scripts. Does not alter user Git configuration.

Set-StrictMode -Version Latest

function Get-QARepoRoot {
  param([string]$ScriptRoot = $PSScriptRoot)
  return (Resolve-Path (Join-Path $ScriptRoot '..\..')).Path
}

function Get-QAVersionLabel {
  param([Parameter(Mandatory = $true)][string]$RepoRoot)
  $versionPath = Join-Path $RepoRoot 'VERSION'
  if (-not (Test-Path -LiteralPath $versionPath)) {
    throw "VERSION file missing at $versionPath"
  }
  return ((Get-Content -LiteralPath $versionPath -Raw) -replace "`r", '').Trim().Trim([char]0xFEFF)
}

function Get-QAEmbeddedVersion {
  param([Parameter(Mandatory = $true)][string]$RepoRoot)
  $embed = Join-Path $RepoRoot 'internal\appinfo\version.txt'
  if (-not (Test-Path -LiteralPath $embed)) {
    throw "internal/appinfo/version.txt missing at $embed"
  }
  return ((Get-Content -LiteralPath $embed -Raw) -replace "`r", '').Trim().Trim([char]0xFEFF)
}

function Sync-QAVersionEmbed {
  param(
    [Parameter(Mandatory = $true)][string]$RepoRoot,
    [Parameter(Mandatory = $true)][string]$Version
  )
  $embed = Join-Path $RepoRoot 'internal\appinfo\version.txt'
  $dir = Split-Path $embed -Parent
  if (-not (Test-Path -LiteralPath $dir)) {
    throw "Missing appinfo directory: $dir"
  }
  $utf8NoBom = New-Object System.Text.UTF8Encoding $false
  [System.IO.File]::WriteAllText($embed, $Version, $utf8NoBom)
}

function Get-QABuildMetadata {
  <#
  .SYNOPSIS
    Derive commit and dirty flag from the working tree. Does not modify files.
  #>
  param(
    [Parameter(Mandatory = $true)][string]$RepoRoot,
    [ValidateSet('development', 'release')]
    [string]$Mode = 'development',
    [string]$BuildTime = ''
  )

  if ([string]::IsNullOrWhiteSpace($BuildTime)) {
    $BuildTime = Format-QABuildTime -Value ([DateTime]::UtcNow)
  } else {
    $BuildTime = Format-QABuildTime -Value $BuildTime
  }

  $meta = [ordered]@{
    Commit       = 'unknown'
    CommitFull   = 'unknown'
    Dirty        = 'true'
    BuildTime    = $BuildTime
    GitAvailable = $false
    HasHEAD      = $false
    DirtyReason  = 'git-unavailable'
    Mode         = $Mode
    ModeLabel    = if ($Mode -eq 'release') { 'release' } else { 'development' }
  }

  $gitCmd = Get-Command git -ErrorAction SilentlyContinue
  if (-not $gitCmd) {
    return [pscustomobject]$meta
  }

  function Invoke-GitQuiet {
    param([Parameter(Mandatory = $true)][string[]]$GitArgs)
    $prev = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
      $out = & git @GitArgs 2>&1
      $code = $LASTEXITCODE
      $text = @($out | ForEach-Object { "$_" }) -join "`n"
      return [pscustomobject]@{ ExitCode = $code; Text = $text.Trim(); Lines = @($out | ForEach-Object { "$_" }) }
    }
    finally {
      $ErrorActionPreference = $prev
    }
  }

  Push-Location -LiteralPath $RepoRoot
  try {
    $inside = Invoke-GitQuiet -GitArgs @('rev-parse', '--is-inside-work-tree')
    if ($inside.ExitCode -ne 0 -or $inside.Text -ne 'true') {
      $meta.DirtyReason = 'not-a-git-worktree'
      return [pscustomobject]$meta
    }
    $meta.GitAvailable = $true

    $full = Invoke-GitQuiet -GitArgs @('rev-parse', 'HEAD')
    if ($full.ExitCode -ne 0 -or [string]::IsNullOrWhiteSpace($full.Text) -or $full.Text -match 'fatal:') {
      $meta.DirtyReason = 'unborn-or-missing-HEAD'
      $meta.Dirty = 'true'
      return [pscustomobject]$meta
    }
    $meta.HasHEAD = $true
    $meta.CommitFull = $full.Text
    $short = Invoke-GitQuiet -GitArgs @('rev-parse', '--short', 'HEAD')
    if ($short.ExitCode -eq 0 -and -not [string]::IsNullOrWhiteSpace($short.Text) -and $short.Text -notmatch 'fatal:') {
      $meta.Commit = $short.Text
    } else {
      $meta.Commit = $meta.CommitFull.Substring(0, [Math]::Min(12, $meta.CommitFull.Length))
    }

    $porcelain = Invoke-GitQuiet -GitArgs @('status', '--porcelain', '--untracked-files=normal')
    if ($porcelain.ExitCode -ne 0) {
      $meta.Dirty = 'true'
      $meta.DirtyReason = 'status-failed'
      return [pscustomobject]$meta
    }
    $dirtyLines = @($porcelain.Lines | Where-Object { $_ -and $_.Trim() -ne '' -and $_ -notmatch '^fatal:' })
    if ($dirtyLines.Count -gt 0) {
      $meta.Dirty = 'true'
      $meta.DirtyReason = 'working-tree-dirty'
    } else {
      $meta.Dirty = 'false'
      $meta.DirtyReason = 'clean'
    }
    return [pscustomobject]$meta
  }
  finally {
    Pop-Location
  }
}

function Assert-QAReleaseMetadata {
  <#
  .SYNOPSIS
    Release preflight. Must run before any source mutation (including version sync).
  #>
  param(
    [Parameter(Mandatory = $true)]$Metadata,
    [Parameter(Mandatory = $true)][string]$RepoRoot,
    [Parameter(Mandatory = $true)][string]$Version
  )
  $errors = New-Object System.Collections.Generic.List[string]
  if (-not $Metadata.GitAvailable) {
    $errors.Add('release build requires Git in PATH and a Git worktree')
  }
  if (-not $Metadata.HasHEAD -or $Metadata.Commit -eq 'unknown' -or $Metadata.CommitFull -eq 'unknown') {
    $errors.Add('release build rejects unknown commit / unborn repository (no HEAD)')
  }
  if ($Metadata.Dirty -ne 'false') {
    $errors.Add("release build requires a clean working tree (reason: $($Metadata.DirtyReason))")
  }
  try {
    $embedded = Get-QAEmbeddedVersion -RepoRoot $RepoRoot
    if ($embedded -ne $Version) {
      $errors.Add("embedded version.txt '$embedded' does not match VERSION '$Version'")
    }
  }
  catch {
    $errors.Add("internal/appinfo/version.txt unreadable before release preflight: $_")
  }
  if ($errors.Count -gt 0) {
    throw ("Release build refused:`n - " + ($errors -join "`n - "))
  }
}

function New-QABuildIdentity {
  <#
  .SYNOPSIS
    Single authoritative build identity for executable + package.
    Release mode: validate Git + VERSION match with NO file mutation.
    Development mode: may sync embedded version after metadata is read.
  #>
  param(
    [Parameter(Mandatory = $true)][string]$RepoRoot,
    [ValidateSet('development', 'release')]
    [string]$Mode = 'development',
    [string]$OS = 'windows',
    [string]$Arch = 'amd64',
    [string]$GoVersion = ''
  )

  if ([string]::IsNullOrWhiteSpace($GoVersion)) {
    $prev = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try { $GoVersion = (& go env GOVERSION 2>$null) } catch { $GoVersion = 'unknown' }
    finally { $ErrorActionPreference = $prev }
    if ([string]::IsNullOrWhiteSpace($GoVersion)) { $GoVersion = 'unknown' }
  }

  $version = Get-QAVersionLabel -RepoRoot $RepoRoot
  # Read Git state BEFORE any synchronization.
  $meta = Get-QABuildMetadata -RepoRoot $RepoRoot -Mode $Mode

  if ($Mode -eq 'release') {
    Assert-QAReleaseMetadata -Metadata $meta -RepoRoot $RepoRoot -Version $version
    # Versions already match and tree is clean — do not rewrite embed.
  }
  else {
    # Development: explicit synchronization after preflight-free metadata capture.
    Sync-QAVersionEmbed -RepoRoot $RepoRoot -Version $version
    $bt = $meta.BuildTime
    $meta = Get-QABuildMetadata -RepoRoot $RepoRoot -Mode $Mode -BuildTime $bt
  }

  return [pscustomobject]@{
    product     = 'QuickAnalyser'
    signature   = 'by Soji'
    version     = $version
    commit      = $meta.Commit
    commit_full = $meta.CommitFull
    dirty       = $meta.Dirty
    build_time  = $meta.BuildTime
    mode        = $meta.ModeLabel
    status      = 'development_unreleased'
    go          = "$GoVersion".Trim()
    os          = $OS
    arch        = $Arch
    copyright   = 'Copyright (c) 2026 Soji'
    license     = 'MIT'
  }
}

function Format-QABuildTime {
  # Always emit invariant UTC RFC3339 (...Z). ConvertFrom-Json may turn ISO strings into DateTime
  # on some hosts; locale ToString() then injects spaces and breaks go -ldflags.
  param([Parameter(Mandatory = $true)]$Value)
  $inv = [System.Globalization.CultureInfo]::InvariantCulture
  $style = [System.Globalization.DateTimeStyles]::AssumeUniversal -bor [System.Globalization.DateTimeStyles]::AdjustToUniversal
  if ($Value -is [datetime]) {
    return ([datetime]$Value).ToUniversalTime().ToString("yyyy-MM-dd'T'HH:mm:ss'Z'", $inv)
  }
  $s = "$Value".Trim()
  if ($s -match '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$') {
    return $s
  }
  $parsed = [datetime]::MinValue
  if ([datetime]::TryParse($s, $inv, $style, [ref]$parsed)) {
    return $parsed.ToUniversalTime().ToString("yyyy-MM-dd'T'HH:mm:ss'Z'", $inv)
  }
  if ([datetime]::TryParse($s, [ref]$parsed)) {
    return $parsed.ToUniversalTime().ToString("yyyy-MM-dd'T'HH:mm:ss'Z'", $inv)
  }
  throw "Unrecognized build_time value: $s"
}

function Get-QALdFlagsFromIdentity {
  param([Parameter(Mandatory = $true)]$Identity)
  $bt = Format-QABuildTime -Value $Identity.build_time
  return "-X quickanalyser/internal/appinfo.Commit=$($Identity.commit) -X quickanalyser/internal/appinfo.Dirty=$($Identity.dirty) -X quickanalyser/internal/appinfo.BuildTime=$bt"
}

function Get-QALdFlags {
  param([Parameter(Mandatory = $true)]$Metadata)
  # Back-compat for scripts still passing raw metadata objects.
  if ($Metadata.PSObject.Properties.Name -contains 'build_time') {
    return (Get-QALdFlagsFromIdentity -Identity $Metadata)
  }
  $commit = $Metadata.Commit
  $dirty = $Metadata.Dirty
  $bt = $Metadata.BuildTime
  return "-X quickanalyser/internal/appinfo.Commit=$commit -X quickanalyser/internal/appinfo.Dirty=$dirty -X quickanalyser/internal/appinfo.BuildTime=$bt"
}

function ConvertTo-QABuildIdentity {
  param([Parameter(Mandatory = $true)][string]$Path)
  $raw = Get-Content -LiteralPath $Path -Raw
  $obj = $raw | ConvertFrom-Json
  # PowerShell ConvertFrom-Json may deserialize RFC3339 as DateTime; normalize before ldflags.
  $obj.build_time = Format-QABuildTime -Value $obj.build_time
  return $obj
}

function Save-QABuildIdentity {
  param(
    [Parameter(Mandatory = $true)]$Identity,
    [Parameter(Mandatory = $true)][string]$Path
  )
  $dir = Split-Path -Parent $Path
  if ($dir -and -not (Test-Path -LiteralPath $dir)) {
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
  }
  # Force string build_time so JSON round-trip stays a quoted RFC3339 value when possible.
  $Identity.build_time = Format-QABuildTime -Value $Identity.build_time
  $utf8NoBom = New-Object System.Text.UTF8Encoding $false
  [System.IO.File]::WriteAllText($Path, ($Identity | ConvertTo-Json -Depth 5), $utf8NoBom)
}

# Package directory / ZIP basename from VERSION + platform + mode.
# Development: QA-v1.0-2026-windows-amd64-dev
# Stable release (known clean commit only): QA-v1.0-2026-windows-amd64
# Commit remains in BUILD_INFO.json / About — never embedded in the ZIP name.
function Get-QAPackageBaseName {
  param(
    [Parameter(Mandatory = $true)]$Identity,
    [switch]$Release
  )
  $base = "$($Identity.version)-$($Identity.os)-$($Identity.arch)"
  # Stable ZIP name only when -Release packaging runs against a known clean commit.
  # Commit/dirty remain in BUILD_INFO.json and About — never invent a commit for the filename.
  if ($Release -and ($Identity.dirty -eq 'false') -and ($Identity.commit -ne 'unknown')) {
    return $base
  }
  return "$base-dev"
}
