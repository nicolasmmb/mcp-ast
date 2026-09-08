#!/usr/bin/env bash
set -euo pipefail

repo="nicolasmmb/mcp-ast"
repo_url="https://github.com/$repo"
api_url="https://api.github.com/repos/$repo"

log() {
  printf '[ast-mcp] %s\n' "$*"
}

fail() {
  log "ERROR: $*" >&2
  exit 1
}

cleanup() {
  [ -z "${tmp_binary:-}" ] || rm -f "$tmp_binary"
  [ -z "${tmp_dir:-}" ] || rm -rf "$tmp_dir"
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

ext=""
[ "$os" = windows ] && ext=".exe"
bin_name="ast-mcp-$os-$arch$ext"
install_dir="$HOME/.local/bin"
dest="$install_dir/ast-mcp$ext"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/ast-mcp.XXXXXX")"

# ---------------------------------------------------------------------------
# Resolve the download: PR artifact (AST_MCP_PR), pinned release
# (AST_MCP_VERSION) or latest release.
# ---------------------------------------------------------------------------
if [ -n "${AST_MCP_PR:-}" ]; then
  log "Step 3/7: Resolving PR #$AST_MCP_PR build."
  command -v jq >/dev/null 2>&1 || fail "jq is required to install a PR build (brew install jq / apt install jq)."
  command -v unzip >/dev/null 2>&1 || fail "unzip is required to install a PR build."

  pr_number="$AST_MCP_PR"
  log "Fetching PR #$pr_number metadata."
  head_sha="$(curl -fsSL "$api_url/pulls/$pr_number" | jq -r '.head.sha // empty')"
  [ -n "$head_sha" ] || fail "Could not resolve PR #$pr_number (does it exist?)."

  log "Finding the latest pr-build run."
  run_id="$(curl -fsSL "$api_url/actions/workflows/pr.yml/runs?head_sha=$head_sha&per_page=20" \
    | jq -r '[.workflow_runs[] | select(.status == "completed" and .conclusion == "success")][0].id // empty')"
  [ -n "$run_id" ] || fail "No successful pr-build run for PR #$pr_number yet. Wait for the PR checks to finish."

  log "Finding artifact $bin_name in run $run_id."
  artifact_id="$(curl -fsSL "$api_url/actions/runs/$run_id/artifacts" \
    | jq -r --arg name "$bin_name" '.artifacts[] | select(.name == $name) | .id' | head -1)"
  [ -n "$artifact_id" ] || fail "Artifact $bin_name not found in run $run_id."

  version="pr-$pr_number"
  log "PR build resolved: $version (run $run_id, artifact $artifact_id)."

  log "Step 4/7: Downloading artifact (zip)."
  if [ -n "${GH_TOKEN:-${GITHUB_TOKEN:-}}" ]; then
    curl -fsSL -H "Authorization: Bearer ${GH_TOKEN:-${GITHUB_TOKEN:-}}" \
      -H "Accept: application/vnd.github+json" \
      "$api_url/actions/artifacts/$artifact_id/zip" -o "$tmp_dir/artifact.zip"
  else
    # nightly.link proxies public artifacts without authentication
    curl -fsSL --retry 3 --retry-delay 1 \
      "https://nightly.link/$repo/actions/runs/$run_id/$bin_name.zip" -o "$tmp_dir/artifact.zip"
  fi
  unzip -q -o "$tmp_dir/artifact.zip" -d "$tmp_dir"
  tmp_binary="$tmp_dir/$bin_name"
  [ -f "$tmp_binary" ] || fail "Artifact zip did not contain $bin_name."

  log "Step 5/7: Verifying SHA-256 checksum."
  expected="$(cut -d ' ' -f1 < "$tmp_dir/$bin_name.sha256" 2>/dev/null || true)"
  [ -n "$expected" ] || fail "Artifact zip did not contain $bin_name.sha256."
else
  log "Step 3/7: Resolving the release."
  if [ -n "${AST_MCP_VERSION:-}" ]; then
    version="$AST_MCP_VERSION"
    log "Using requested version: $version."
  else
    release_url="$(curl -fsSL -o /dev/null -w '%{url_effective}' "$repo_url/releases/latest")"
    version="${release_url##*/}"
    [ -n "$version" ] && [ "$version" != "latest" ] || fail "Could not determine the latest release version."
    log "Latest release: $version."
  fi

  asset_url="$repo_url/releases/download/$version/$bin_name"
  checksum_url="$asset_url.sha256"

  log "Step 4/7: Downloading $bin_name."
  tmp_binary="$tmp_dir/$bin_name"
  curl -fsSL --retry 3 --retry-delay 1 -o "$tmp_binary" "$asset_url"

  log "Step 5/7: Verifying SHA-256 checksum."
  expected="$(curl -fsSL "$checksum_url" | cut -d ' ' -f1)"
  [ -n "$expected" ] || fail "Release checksum is empty."
fi

if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp_binary" | cut -d ' ' -f1)"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "$tmp_binary" | cut -d ' ' -f1)"
else
  fail "A SHA-256 tool (sha256sum or shasum) is required."
fi
[ "$actual" = "$expected" ] || fail "Checksum mismatch: expected $expected, got $actual."
log "Checksum verified."

size="$(wc -c < "$tmp_binary" | tr -d '[:space:]')"

log "Step 6/7: Installing to $dest."
mkdir -p "$install_dir"
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
