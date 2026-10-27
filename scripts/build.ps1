# Development or release Windows build.
# Release: .\scripts\build.ps1 -Release  (validates Git BEFORE any file mutation)
# Packaging may pass -IdentityFile to reuse one authoritative identity.
param(
  [switch]$Release,
  [switch]$PreflightOnly,
  [string]$RepoRoot = '',
  [string]$IdentityFile = '',
  [string]$WriteIdentityFile = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

. (Join-Path $PSScriptRoot 'lib\Get-QABuildMetadata.ps1')

if ([string]::IsNullOrWhiteSpace($RepoRoot)) {
  $RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
}
Set-Location -LiteralPath $RepoRoot

$mode = if ($Release) { 'release' } else { 'development' }

if (-not [string]::IsNullOrWhiteSpace($IdentityFile)) {
  if (-not (Test-Path -LiteralPath $IdentityFile)) {
    throw "IdentityFile not found: $IdentityFile"
  }
  $identity = ConvertTo-QABuildIdentity -Path $IdentityFile
  if ($Release) {
    # Re-validate live tree against the provided identity without mutating sources.
    $live = Get-QABuildMetadata -RepoRoot $RepoRoot -Mode release -BuildTime $identity.build_time
    Assert-QAReleaseMetadata -Metadata $live -RepoRoot $RepoRoot -Version $identity.version
    if ($live.CommitFull -ne $identity.commit_full -and $identity.commit_full -ne 'unknown') {
      throw "Identity commit $($identity.commit_full) does not match live HEAD $($live.CommitFull)"
    }
  }
}
else {
  $identity = New-QABuildIdentity -RepoRoot $RepoRoot -Mode $mode
}

if (-not [string]::IsNullOrWhiteSpace($WriteIdentityFile)) {
  Save-QABuildIdentity -Identity $identity -Path $WriteIdentityFile
}

Write-Host ("Identity version={0} commit={1} dirty={2} build_time={3} mode={4} status={5}" -f `
  $identity.version, $identity.commit, $identity.dirty, $identity.build_time, $identity.mode, $identity.status)

if ($PreflightOnly) {
  Write-Host 'PreflightOnly: skipping go build'
  exit 0
}

New-Item -ItemType Directory -Force -Path (Join-Path $RepoRoot 'dist') | Out-Null
$ld = Get-QALdFlagsFromIdentity -Identity $identity
$out = Join-Path $RepoRoot 'dist\QuickAnalyser.exe'
Write-Host "Building ($($identity.mode)) → $out"
# Pass ldflags as a single argv element so embedded values cannot split the linker command line.
& go build ("-ldflags=$ld") -o $out ./cmd/quickanalyser
if ($LASTEXITCODE -ne 0) {
  throw "go build failed with exit code $LASTEXITCODE"
}
Write-Host "Built $out ($($identity.version) $($identity.mode) · $($identity.commit) · dirty=$($identity.dirty) · $($identity.build_time))"
