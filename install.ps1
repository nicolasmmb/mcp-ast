$ErrorActionPreference = 'Stop'

$repo = 'nicolasmmb/mcp-ast'
$base = "https://github.com/$repo/releases/latest/download"

$arch = $env:PROCESSOR_ARCHITECTURE
switch ($arch) {
  'AMD64' { $goarch = 'amd64' }
  'ARM64' { $goarch = 'arm64' }
  default { throw "arquitetura nao suportada: $arch" }
}

$asset = "ast-mcp-windows-$goarch.exe"
$dir = Join-Path $env:LOCALAPPDATA 'ast-mcp'
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$dest = Join-Path $dir 'ast-mcp.exe'

Write-Host "baixando $asset ..."
Invoke-WebRequest -Uri "$base/$asset" -OutFile $dest

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($userPath -notlike "*$dir*") {
  [Environment]::SetEnvironmentVariable('Path', "$dir;$userPath", 'User')
  Write-Host "adicionado ao PATH do usuario: $dir"
} else {
  Write-Host "$dir ja esta no PATH."
}

Write-Host "instalado: $dest"
Write-Host "reabra o terminal para usar o comando 'ast-mcp'."
