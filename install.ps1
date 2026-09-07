$ErrorActionPreference = 'Stop'

$repo = 'nicolasmmb/mcp-ast'
$repoUrl = "https://github.com/$repo"
$apiUrl = "https://api.github.com/repos/$repo/releases/latest"

function Write-Log([string]$Message) {
  Write-Host "[ast-mcp] $Message"
}

try {
  Write-Log 'Step 1/7: Checking prerequisites.'
  if (-not (Get-Command Invoke-WebRequest -ErrorAction SilentlyContinue)) {
    throw 'Invoke-WebRequest is required to download ast-mcp.'
  }

  Write-Log 'Step 2/7: Detecting platform.'
  if ($env:OS -ne 'Windows_NT') {
    throw "Unsupported operating system: $([Environment]::OSVersion.Platform). Use install.sh on Linux or macOS."
  }
  $nativeArch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
  switch ($nativeArch) {
    'AMD64' { $goarch = 'amd64' }
    'ARM64' { $goarch = 'arm64' }
    default { throw "Unsupported architecture: $nativeArch" }
  }
  Write-Log "Platform detected: windows/$goarch."

  Write-Log 'Step 3/7: Resolving the latest release.'
  $release = Invoke-RestMethod -Uri $apiUrl
  $version = $release.tag_name
  if ([string]::IsNullOrWhiteSpace($version)) {
    throw 'Could not determine the latest release version.'
  }
  Write-Log "Latest release: $version."

  $asset = "ast-mcp-windows-$goarch.exe"
  $assetUrl = "$repoUrl/releases/download/$version/$asset"
  $checksumUrl = "$assetUrl.sha256"
  $dir = Join-Path $env:LOCALAPPDATA 'ast-mcp'
  $dest = Join-Path $dir 'ast-mcp.exe'
  $tmp = Join-Path ([System.IO.Path]::GetTempPath()) "ast-mcp-$PID.exe"

  Write-Log "Step 4/7: Downloading $asset."
  New-Item -ItemType Directory -Force -Path $dir | Out-Null
  Invoke-WebRequest -Uri $assetUrl -OutFile $tmp
  $size = (Get-Item $tmp).Length
  Write-Log "Downloaded $size bytes."

  Write-Log 'Step 5/7: Verifying SHA-256 checksum.'
  $expected = ((Invoke-WebRequest -Uri $checksumUrl).Content -split '\s+')[0]
  if ([string]::IsNullOrWhiteSpace($expected)) {
    throw 'Release checksum is empty.'
  }
  $actual = (Get-FileHash -Algorithm SHA256 -Path $tmp).Hash.ToLowerInvariant()
  if ($actual -ne $expected.ToLowerInvariant()) {
    throw "Checksum mismatch: expected $expected, got $actual."
  }
  Write-Log 'Checksum verified.'

  Write-Log "Step 6/7: Installing to $dest."
  Move-Item -Force -Path $tmp -Destination $dest
  Write-Log "Installed $dest ($size bytes)."

  Write-Log 'Step 7/7: Configuring PATH.'
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  $pathEntries = @($userPath -split ';' | Where-Object { $_ })
  if ($pathEntries -notcontains $dir) {
    $newUserPath = (@($dir) + $pathEntries) -join ';'
    [Environment]::SetEnvironmentVariable('Path', $newUserPath, 'User')
    Write-Log "Added $dir to the user PATH."
  } else {
    Write-Log "$dir is already in the user PATH."
  }
  if ($env:Path -notlike "*$dir*") {
    $env:Path = "$dir;$env:Path"
  }

  Write-Log "Installation complete: ast-mcp $version for windows/$goarch."
  Write-Log 'Open a new terminal before running ast-mcp.'
}
catch {
  Write-Error "[ast-mcp] ERROR: Installation failed: $($_.Exception.Message)"
  exit 1
}
finally {
  if ($tmp -and (Test-Path $tmp)) {
    Remove-Item -Force $tmp -ErrorAction SilentlyContinue
  }
}
