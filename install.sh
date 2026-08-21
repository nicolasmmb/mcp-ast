#!/usr/bin/env bash
set -euo pipefail

repo="nicolasmmb/mcp-ast"
base="https://github.com/$repo/releases/latest/download"

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux)  os=linux ;;
  MINGW*|MSYS*|CYGWIN*) os=windows ;;
  *) echo "erro: sistema nao suportado: $(uname -s)" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64|amd64)  arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "erro: arquitetura nao suportada: $(uname -m)" >&2; exit 1 ;;
esac

ext=""
[ "$os" = windows ] && ext=".exe"
asset="ast-mcp-$os-$arch$ext"

if [ "$os" = windows ]; then
  dir="$HOME/.local/bin"
else
  dir=/usr/local/bin
  [ -w "$dir" ] || dir="$HOME/.local/bin"
fi
mkdir -p "$dir"

echo "baixando $asset da release latest..."
curl -fsSL -o "$dir/ast-mcp$ext" "$base/$asset"
chmod +x "$dir/ast-mcp$ext"
echo "instalado: $dir/ast-mcp$ext ($(wc -c < "$dir/ast-mcp$ext") bytes)"

if printf '%s' "$PATH" | tr ':' '\n' | grep -qx "$dir"; then
  echo "$dir ja esta no PATH."
else
  case "${SHELL##*/}" in
    zsh)  profile="${ZDOTDIR:-$HOME}/.zshrc" ;;
    *)    profile="$HOME/.bashrc" ;;
  esac
  printf '\n# ast-mcp\nexport PATH="%s:$PATH"\n' "$dir" >> "$profile"
  echo "adicionado ao PATH em $profile."
  echo "rode: source $profile   (ou reabra o terminal)"
fi

echo "pronto. configure o cliente MCP para usar o comando 'ast-mcp'."
