# Validate local relative Markdown links under a root directory (e.g. repo or extracted ZIP).
Set-StrictMode -Version Latest

function Get-QAMarkdownLocalTargets {
  param(
    [Parameter(Mandatory = $true)][string]$MarkdownPath
  )
  $text = Get-Content -LiteralPath $MarkdownPath -Raw -ErrorAction Stop
  $targets = New-Object System.Collections.Generic.List[string]
  foreach ($m in [regex]::Matches($text, '\[[^\]]*\]\(([^)]+)\)')) {
    $u = $m.Groups[1].Value.Trim()
    if ([string]::IsNullOrWhiteSpace($u)) { continue }
    if ($u -match '^(?i)(https?:|mailto:|data:)') { continue }
    # Strip optional title in quotes after the path.
    if ($u -match '^<([^>]+)>') { $u = $Matches[1] }
    elseif ($u -match '^([^\s]+)') { $u = $Matches[1] }
    $pathOnly = ($u -replace '#.*$', '').Trim()
    if ([string]::IsNullOrWhiteSpace($pathOnly)) { continue }
    $targets.Add($pathOnly) | Out-Null
  }
  return $targets
}

function Test-QAMarkdownLinksInRoot {
  param(
    [Parameter(Mandatory = $true)][string]$Root,
    [string[]]$Files = @(),
    [string[]]$RequiredFromReadme = @()
  )
  $rootFull = (Resolve-Path -LiteralPath $Root).Path
  $failures = New-Object System.Collections.Generic.List[string]
  $resolvedGuides = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::OrdinalIgnoreCase)

  if ($Files.Count -eq 0) {
    $Files = @(Get-ChildItem -LiteralPath $rootFull -Recurse -Filter '*.md' -File | ForEach-Object {
      $_.FullName.Substring($rootFull.Length).TrimStart('\', '/').Replace('\', '/')
    })
  }

  foreach ($rel in $Files) {
    $mdPath = Join-Path $rootFull ($rel -replace '/', '\')
    if (-not (Test-Path -LiteralPath $mdPath)) {
      $failures.Add("missing markdown file: $rel")
      continue
    }
    $dir = Split-Path -Parent $mdPath
    foreach ($target in (Get-QAMarkdownLocalTargets -MarkdownPath $mdPath)) {
      $candidate = [System.IO.Path]::GetFullPath((Join-Path $dir ($target -replace '/', '\')))
      if (-not $candidate.StartsWith($rootFull, [System.StringComparison]::OrdinalIgnoreCase)) {
        $failures.Add("${rel}: link escapes root -> $target")
        continue
      }
      if (-not (Test-Path -LiteralPath $candidate)) {
        $failures.Add("${rel}: broken link -> $target")
        continue
      }
      $leaf = Split-Path -Leaf $candidate
      if ($leaf -match '^(?i)(QUICK_START|USER_GUIDE|TROUBLESHOOTING)\.md$') {
        [void]$resolvedGuides.Add($leaf)
      }
    }
  }

  foreach ($need in $RequiredFromReadme) {
    if (-not $resolvedGuides.Contains($need)) {
      $failures.Add("README does not resolve to packaged guide: $need")
    }
  }

  return [pscustomobject]@{
    Failures = @($failures)
    ResolvedGuides = @($resolvedGuides)
  }
}
