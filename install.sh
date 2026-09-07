#!/usr/bin/env bash
set -euo pipefail

repo="nicolasmmb/mcp-ast"
repo_url="https://github.com/$repo"

log() {
  printf '[ast-mcp] %s\n' "$*"
}

fail() {
  log "ERROR: $*" >&2
  exit 1
}

cleanup() {
  [ -z "${tmp_binary:-}" ] || rm -f "$tmp_binary"
}

on_error() {
  status=$?
  log "ERROR: Installation failed (exit code $status)." >&2
  exit "$status"
}

trap cleanup EXIT
trap on_error ERR

log "Step 1/7: Checking prerequisites."
command -v curl >/dev/null 2>&1 || fail "curl is required to download ast-mcp."

log "Step 2/7: Detecting platform."
case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  MINGW*|MSYS*|CYGWIN*) os=windows ;;
  *) fail "Unsupported operating system: $(uname -s)." ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) fail "Unsupported architecture: $(uname -m)." ;;
esac
log "Platform detected: $os/$arch."

log "Step 3/7: Resolving the latest release."
release_url="$(curl -fsSL -o /dev/null -w '%{url_effective}' "$repo_url/releases/latest")"
version="${release_url##*/}"
[ -n "$version" ] && [ "$version" != "latest" ] || fail "Could not determine the latest release version."
log "Latest release: $version."

ext=""
[ "$os" = windows ] && ext=".exe"
asset="ast-mcp-$os-$arch$ext"
asset_url="$repo_url/releases/download/$version/$asset"
checksum_url="$asset_url.sha256"

install_dir="$HOME/.local/bin"
dest="$install_dir/ast-mcp$ext"

log "Step 4/7: Downloading $asset."
mkdir -p "$install_dir"
tmp_binary="$(mktemp "${TMPDIR:-/tmp}/ast-mcp.XXXXXX")"
curl -fsSL --retry 3 --retry-delay 1 -o "$tmp_binary" "$asset_url"
size="$(wc -c < "$tmp_binary" | tr -d '[:space:]')"
log "Downloaded $size bytes."

log "Step 5/7: Verifying SHA-256 checksum."
expected="$(curl -fsSL "$checksum_url" | cut -d ' ' -f1)"
[ -n "$expected" ] || fail "Release checksum is empty."
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp_binary" | cut -d ' ' -f1)"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "$tmp_binary" | cut -d ' ' -f1)"
else
  fail "A SHA-256 tool (sha256sum or shasum) is required."
fi
[ "$actual" = "$expected" ] || fail "Checksum mismatch: expected $expected, got $actual."
log "Checksum verified."

log "Step 6/7: Installing to $dest."
mv "$tmp_binary" "$dest"
tmp_binary=""
chmod +x "$dest"
log "Installed $dest ($size bytes)."

log "Step 7/7: Configuring PATH."
case ":$PATH:" in
  *":$install_dir:"*) log "$install_dir is already in PATH." ;;
  *)
    shell_name="${SHELL:-bash}"
    shell_name="${shell_name##*/}"
    case "$shell_name" in
      zsh) profile="${ZDOTDIR:-$HOME}/.zshrc" ;;
      fish) profile="$HOME/.config/fish/config.fish" ;;
      *) profile="$HOME/.bashrc" ;;
    esac
    if [ "$shell_name" = fish ]; then
      path_line="fish_add_path $install_dir"
    else
      path_line="export PATH=\"$install_dir:\$PATH\""
    fi
    if [ -f "$profile" ] && grep -Fqx "$path_line" "$profile"; then
      log "PATH entry already exists in $profile."
    else
      mkdir -p "$(dirname "$profile")"
      printf '\n# ast-mcp\n%s\n' "$path_line" >> "$profile"
      log "Added $install_dir to PATH in $profile."
    fi
    log "Open a new terminal or reload $profile before running ast-mcp."
    ;;
esac

log "Installation complete: ast-mcp $version for $os/$arch."
