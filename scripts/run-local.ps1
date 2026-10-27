$ErrorActionPreference = 'Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)
if (-not (Test-Path 'dist/QuickAnalyser.exe')) { & "$PSScriptRoot/build.ps1" }
& './dist/QuickAnalyser.exe'
