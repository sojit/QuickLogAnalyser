# Local verification gate: vet, tests, Highlights markup, then build with accurate metadata.
# Release mode validates Git cleanliness and VERSION match before any source mutation.
param(
  [switch]$Release,
  [string]$RepoRoot = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

. (Join-Path $PSScriptRoot 'lib\Get-QABuildMetadata.ps1')

if ([string]::IsNullOrWhiteSpace($RepoRoot)) {
  $RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
}
Set-Location -LiteralPath $RepoRoot

function Invoke-Checked {
  param([string]$Label, [scriptblock]$Command)
  Write-Host $Label
  & $Command
  if ($null -ne $LASTEXITCODE -and $LASTEXITCODE -ne 0) {
    throw "$Label failed with exit code $LASTEXITCODE"
  }
}

Invoke-Checked 'go vet ./...' { go vet ./... }
Invoke-Checked 'go test ./...' { go test ./... }

$node = Get-Command node -ErrorAction SilentlyContinue
if ($node) {
  Invoke-Checked 'node scripts/highlights_markup_test.cjs' {
    node (Join-Path $RepoRoot 'scripts\highlights_markup_test.cjs')
  }
} else {
  Write-Host 'node not found — running Go Highlights structure test instead'
  Invoke-Checked 'go test ./web -run TestHighlightsLayoutTwoIssuesOneFile' {
    go test ./web -count=1 -run TestHighlightsLayoutTwoIssuesOneFile
  }
}

$mode = if ($Release) { 'release' } else { 'development' }
$identity = New-QABuildIdentity -RepoRoot $RepoRoot -Mode $mode
$identityPath = Join-Path $RepoRoot 'dist\build-identity.json'
Save-QABuildIdentity -Identity $identity -Path $identityPath

New-Item -ItemType Directory -Force -Path (Join-Path $RepoRoot 'dist') | Out-Null
$ld = Get-QALdFlagsFromIdentity -Identity $identity
Write-Host ("go build ({0}) commit={1} dirty={2} build_time={3}" -f $identity.mode, $identity.commit, $identity.dirty, $identity.build_time)
Invoke-Checked 'go build' {
  go build -ldflags $ld -o (Join-Path $RepoRoot 'dist\QuickAnalyser.exe') ./cmd/quickanalyser
}
Write-Host "verify OK ($($identity.mode) · dirty=$($identity.dirty) · $($identity.commit) · $($identity.build_time))"
