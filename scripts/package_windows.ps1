# Assemble a portable Windows package under dist/package/ (not a published release).
# One New-QABuildIdentity is shared by the executable and BUILD_INFO.json.
#
# ZIP / stage names (no commit slug):
#   Development: QA-v1.0-2026-windows-amd64-dev.zip
#   Approved stable (-Release, known clean commit): QA-v1.0-2026-windows-amd64.zip
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

$mode = if ($Release) { 'release' } else { 'development' }

# Single authoritative identity — release preflight runs here before any mutation.
$identity = New-QABuildIdentity -RepoRoot $RepoRoot -Mode $mode
$identityPath = Join-Path $RepoRoot 'dist\build-identity.json'
Save-QABuildIdentity -Identity $identity -Path $identityPath

$buildScript = Join-Path $PSScriptRoot 'build.ps1'
$buildArgs = @{
  IdentityFile = $identityPath
  RepoRoot     = $RepoRoot
}
if ($Release) { $buildArgs['Release'] = $true }
& $buildScript @buildArgs

$stageName = Get-QAPackageBaseName -Identity $identity -Release:$Release
$packageRoot = Join-Path $RepoRoot 'dist\package'
if (-not (Test-Path -LiteralPath $packageRoot)) {
  New-Item -ItemType Directory -Force -Path $packageRoot | Out-Null
}
$packageRootFull = (Resolve-Path -LiteralPath $packageRoot).Path
$stage = Join-Path $packageRootFull $stageName
$stageFull = [System.IO.Path]::GetFullPath($stage)
if (-not $stageFull.StartsWith($packageRootFull, [System.StringComparison]::OrdinalIgnoreCase)) {
  throw "Refusing package stage outside dist/package: $stageFull"
}
if (Test-Path -LiteralPath $stageFull) {
  Write-Host "Replacing existing package stage: $stageFull"
  Remove-Item -Recurse -Force -LiteralPath $stageFull
}
New-Item -ItemType Directory -Force -Path $stageFull | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $stageFull 'licenses') | Out-Null

$docsUser = Join-Path $RepoRoot 'docs\user'
# Package README uses root-relative guide links; repo README.md keeps docs/user/ links.
$requiredDocs = @(
  @{ Src = (Join-Path $RepoRoot 'dist\QuickAnalyser.exe'); Dest = 'QuickAnalyser.exe' },
  @{ Src = (Join-Path $docsUser 'QUICK_START.md'); Dest = 'QUICK_START.md' },
  @{ Src = (Join-Path $docsUser 'USER_GUIDE.md'); Dest = 'USER_GUIDE.md' },
  @{ Src = (Join-Path $docsUser 'TROUBLESHOOTING.md'); Dest = 'TROUBLESHOOTING.md' },
  @{ Src = (Join-Path $docsUser 'SYSTEM_REQUIREMENTS.md'); Dest = 'SYSTEM_REQUIREMENTS.md' },
  @{ Src = (Join-Path $docsUser 'PACKAGE_README.md'); Dest = 'README.md' },
  @{ Src = (Join-Path $RepoRoot 'VERSION'); Dest = 'VERSION' },
  @{ Src = (Join-Path $RepoRoot 'LICENSE'); Dest = 'LICENSE' },
  @{ Src = (Join-Path $RepoRoot 'THIRD_PARTY_NOTICES.md'); Dest = 'THIRD_PARTY_NOTICES.md' }
)
foreach ($item in $requiredDocs) {
  if (-not (Test-Path -LiteralPath $item.Src)) {
    throw "Required package payload missing: $($item.Src)"
  }
  Copy-Item -LiteralPath $item.Src -Destination (Join-Path $stageFull $item.Dest)
}
if (Test-Path (Join-Path $RepoRoot 'licenses')) {
  Copy-Item -Recurse (Join-Path $RepoRoot 'licenses\*') (Join-Path $stageFull 'licenses\')
}

# Same identity object as the executable ldflags (not a second metadata capture).
Save-QABuildIdentity -Identity $identity -Path (Join-Path $stageFull 'BUILD_INFO.json')

$exe = Join-Path $stageFull 'QuickAnalyser.exe'
$hash = (Get-FileHash -LiteralPath $exe -Algorithm SHA256).Hash
$sums = New-Object System.Collections.Generic.List[string]
$sums.Add("$hash  QuickAnalyser.exe")
Get-ChildItem -LiteralPath $stageFull -Recurse -File | Where-Object { $_.Name -ne 'SHA256SUMS.txt' } | ForEach-Object {
  $rel = $_.FullName.Substring($stageFull.Length).TrimStart('\', '/').Replace('\', '/')
  if ($rel -eq 'QuickAnalyser.exe') { return }
  $h = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash
  $sums.Add("$h  $rel")
}
($sums -join "`n") + "`n" | Set-Content -LiteralPath (Join-Path $stageFull 'SHA256SUMS.txt') -Encoding ascii -NoNewline

$distRoot = Join-Path $RepoRoot 'dist'
$distRootFull = (Resolve-Path -LiteralPath $distRoot).Path
$zip = Join-Path $distRootFull ($stageName + '.zip')
$zipFull = [System.IO.Path]::GetFullPath($zip)
if (-not $zipFull.StartsWith($distRootFull, [System.StringComparison]::OrdinalIgnoreCase)) {
  throw "Refusing ZIP path outside dist/: $zipFull"
}
if (Test-Path -LiteralPath $zipFull) {
  Write-Host "Replacing existing ZIP: $zipFull"
  Remove-Item -Force -LiteralPath $zipFull
}
$zipSidecar = $zipFull + '.sha256'
if (Test-Path -LiteralPath $zipSidecar) {
  Remove-Item -Force -LiteralPath $zipSidecar
}
Compress-Archive -Path (Join-Path $stageFull '*') -DestinationPath $zipFull -Force
$zipHash = (Get-FileHash -LiteralPath $zipFull -Algorithm SHA256).Hash
$utf8NoBom = New-Object System.Text.UTF8Encoding $false
[System.IO.File]::WriteAllText($zipSidecar, "$zipHash  $(Split-Path $zipFull -Leaf)`n", $utf8NoBom)

Write-Host "Package stage: $stageFull"
Write-Host "Zip: $zipFull"
Write-Host "Zip SHA-256: $zipHash"
Write-Host "EXE SHA-256: $hash"
Write-Host "Identity build_time=$($identity.build_time) commit=$($identity.commit) dirty=$($identity.dirty) status=$($identity.status)"
Write-Host "Status remains development_unreleased until qualification sign-off."
