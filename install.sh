#!/usr/bin/env bash

set -euo pipefail

OS="$(uname -s)"
GITHUB_REPO="notliad/liftoff"

INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
BIN_PATH="$INSTALL_DIR/lo"
MAN_DIR="${MAN_DIR:-$HOME/.local/share/man/man1}"
MAN_PATH="$MAN_DIR/lo.1"
GO_MODULE_DEFAULT="${GO_MODULE_DEFAULT:-github.com/notliad/liftoff/cmd/lo@latest}"

usage() {
  cat <<'EOF'
install.sh - install lo CLI

Usage:
  bash install.sh                          # auto: binary download → local build → go install
  bash install.sh --from-binary            # download pre-built binary from GitHub Releases
  bash install.sh --from-local             # build from ./cmd/lo (requires Go)
  bash install.sh --from-module [mod@ver]  # go install (requires Go)
  bash install.sh --man-from-url <url>     # also fetch man page from a raw URL base
  bash install.sh --uninstall
  bash install.sh --help

Examples:
  bash install.sh
  bash install.sh --from-binary
  bash install.sh --from-local
  bash install.sh --from-module github.com/notliad/liftoff/cmd/lo@latest
  bash install.sh --from-module github.com/notliad/liftoff/cmd/lo@main \
    --man-from-url https://raw.githubusercontent.com/notliad/liftoff/main

Notes:
  - default mode downloads a pre-built binary; no Go required.
  - falls back to local build or go install when no binary is available.
  - installs man page to ~/.local/share/man/man1/lo.1 when available.
EOF
}

# --- Platform detection ---

detect_platform() {
  local os arch
  case "$OS" in
    Linux)  os="linux"  ;;
    Darwin) os="darwin" ;;
    *)
      printf "[warn] Unsupported OS for binary download: %s\n" "$OS" >&2
      return 1
      ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64)  arch="amd64" ;;
    arm64|aarch64) arch="arm64" ;;
    *)
      printf "[warn] Unknown arch: %s — assuming amd64\n" "$(uname -m)" >&2
      arch="amd64"
      ;;
  esac
  printf "%s-%s" "$os" "$arch"
}

# Emit stdout from url; prefers curl, falls back to wget.
http_get() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO- "$1"
  else
    printf "[error] curl or wget is required\n" >&2
    return 1
  fi
}

# Download url to file dest.
http_download() {
  local url="$1" dest="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$dest"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$dest" "$url"
  else
    printf "[error] curl or wget is required\n" >&2
    return 1
  fi
}

# --- Binary install ---

install_from_binary() {
  local platform
  platform="$(detect_platform)" || return 1

  local asset_name="lo-${platform}"
  local api_url="https://api.github.com/repos/${GITHUB_REPO}/releases/latest"

  local release_json
  release_json="$(http_get "$api_url" 2>/dev/null)" || {
    printf "[warn] Could not reach GitHub API\n" >&2
    return 1
  }

  local download_url
  download_url="$(printf '%s' "$release_json" \
    | grep -o "\"browser_download_url\": \"[^\"]*${asset_name}\"" \
    | head -1 \
    | cut -d'"' -f4)"

  if [ -z "$download_url" ]; then
    printf "[warn] No pre-built binary found for %s in latest release\n" "$platform" >&2
    return 1
  fi

  mkdir -p "$INSTALL_DIR"
  http_download "$download_url" "$BIN_PATH"
  chmod +x "$BIN_PATH"
  printf "Installed lo (%s) to %s\n" "$platform" "$BIN_PATH"
  install_man_local || true
  return 0
}

ensure_path_hint() {
  case ":$PATH:" in
    *":$INSTALL_DIR:"*)
      ;;
    *)
      printf "\n[info] Add this to your shell config if needed:\n"
      printf "export PATH=\"%s:\$PATH\"\n" "$INSTALL_DIR"
      ;;
  esac

  if [ -n "${MANPATH:-}" ] && [[ ":$MANPATH:" != *":${HOME}/.local/share/man:"* ]]; then
    printf "[info] Optional MANPATH entry:\n"
    printf "export MANPATH=\"$HOME/.local/share/man:\$MANPATH\"\n"
  fi
}

refresh_man_db() {
  # mandb is Linux (man-db) only; macOS rebuilds whatis automatically
  if [[ "$OS" == "Linux" ]] && command -v mandb >/dev/null 2>&1; then
    mandb -q "$HOME/.local/share/man" >/dev/null 2>&1 || true
  fi
}

install_man_local() {
  if [ ! -f "./man/man1/lo.1" ]; then
    return 1
  fi

  mkdir -p "$MAN_DIR"
  cp "./man/man1/lo.1" "$MAN_PATH"
  printf "Installed man page to %s\n" "$MAN_PATH"
  refresh_man_db
  return 0
}

install_man_from_url() {
  local base_url="$1"
  local man_url="${base_url%/}/man/man1/lo.1"

  if ! command -v curl >/dev/null 2>&1; then
    printf "[warn] curl is required to download man page from URL\n" >&2
    return 1
  fi

  mkdir -p "$MAN_DIR"
  if curl -fsSL "$man_url" -o "$MAN_PATH"; then
    printf "Installed man page from %s\n" "$man_url"
    refresh_man_db
    return 0
  fi

  printf "[warn] Could not download man page from %s\n" "$man_url" >&2
  return 1
}

require_go() {
  if ! command -v go >/dev/null 2>&1; then
    printf "❌ Go is required for installation.\n" >&2
    exit 1
  fi
}

install_from_local() {
  if [ ! -f "./go.mod" ] || [ ! -d "./cmd/lo" ]; then
    return 1
  fi

  require_go
  mkdir -p "$INSTALL_DIR"
  go build -o "$BIN_PATH" ./cmd/lo
  printf "Installed local build to %s\n" "$BIN_PATH"
  install_man_local || true
  return 0
}

install_from_module() {
  local module="$1"

  require_go
  mkdir -p "$INSTALL_DIR"
  GOBIN="$INSTALL_DIR" go install "$module"
  printf "Installed module %s to %s\n" "$module" "$BIN_PATH"
  return 0
}

uninstall() {
  rm -f "$BIN_PATH"
  rm -f "$MAN_PATH"
  printf "Removed %s\n" "$BIN_PATH"
  printf "Removed %s\n" "$MAN_PATH"
  printf "To remove config too, run: rm -rf ~/.config/lo\n"
}

MODE="auto"
MODULE="$GO_MODULE_DEFAULT"
MAN_URL_BASE=""

while [ "$#" -gt 0 ]; do
  case "$1" in
    --from-binary)
      MODE="binary"
      ;;
    --from-local)
      MODE="local"
      ;;
    --from-module)
      MODE="module"
      shift
      if [ "$#" -eq 0 ]; then
        printf "--from-module requires a value\n" >&2
        exit 1
      fi
      MODULE="$1"
      ;;
    --man-from-url)
      shift
      if [ "$#" -eq 0 ]; then
        printf "--man-from-url requires a value\n" >&2
        exit 1
      fi
      MAN_URL_BASE="$1"
      ;;
    --uninstall)
      uninstall
      exit 0
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      printf "Unknown argument: %s\n" "$1" >&2
      usage
      exit 1
      ;;
  esac
  shift
done

case "$MODE" in
  binary)
    install_from_binary || {
      printf "Binary download failed. Re-run with --from-local or --from-module to install from source.\n" >&2
      exit 1
    }
    ;;
  local)
    install_from_local || {
      printf "Could not build from local source (expected ./go.mod and ./cmd/lo)\n" >&2
      exit 1
    }
    ;;
  module)
    if [ -z "$MODULE" ]; then
      printf "Missing module\n" >&2
      exit 1
    fi
    install_from_module "$MODULE"
    ;;
  auto)
    # Prefer pre-built binary; fall back to local build, then go install.
    if install_from_binary; then
      :
    elif install_from_local; then
      :
    else
      install_from_module "$MODULE"
    fi
    ;;
esac

if [ -n "$MAN_URL_BASE" ]; then
  install_man_from_url "$MAN_URL_BASE" || true
fi

if command -v lo >/dev/null 2>&1; then
  printf "\nlo is available: %s\n" "$(command -v lo)"
else
  printf "\nInstall completed at %s\n" "$BIN_PATH"
fi

ensure_path_hint
printf "Run 'lo --help' to get started.\n"
printf "Run 'man lo' for the manual.\n"
