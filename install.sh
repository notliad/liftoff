#!/usr/bin/env bash

set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
BIN_PATH="$INSTALL_DIR/lo"

usage() {
  cat <<'EOF'
install.sh - install lo CLI

Usage:
  bash install.sh
  bash install.sh --from-local
  bash install.sh --from-url <raw-base-url>
  bash install.sh --uninstall
  bash install.sh --help

Examples:
  bash install.sh --from-local
  bash install.sh --from-url https://raw.githubusercontent.com/you/lo/main

Notes:
  - --from-local copies ./lo from this directory.
  - --from-url downloads <raw-base-url>/lo.
  - default mode installs from local file only.
EOF
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
}

install_from_local() {
  if [ ! -f "./lo" ]; then
    return 1
  fi

  mkdir -p "$INSTALL_DIR"
  cp "./lo" "$BIN_PATH"
  chmod +x "$BIN_PATH"
  printf "Installed from local file to %s\n" "$BIN_PATH"
  return 0
}

install_from_url() {
  local base_url="$1"
  local source_url="${base_url%/}/lo"

  if ! command -v curl >/dev/null 2>&1; then
    printf "curl is required to install from URL.\n" >&2
    return 1
  fi

  mkdir -p "$INSTALL_DIR"
  curl -fsSL "$source_url" -o "$BIN_PATH"
  chmod +x "$BIN_PATH"
  printf "Installed from %s to %s\n" "$source_url" "$BIN_PATH"
  return 0
}

uninstall() {
  rm -f "$BIN_PATH"
  printf "Removed %s\n" "$BIN_PATH"
  printf "To remove config too, run: rm -rf ~/.config/lo\n"
}

MODE="auto"
URL_BASE=""

while [ "$#" -gt 0 ]; do
  case "$1" in
    --from-local)
      MODE="local"
      ;;
    --from-url)
      MODE="url"
      shift
      if [ "$#" -eq 0 ]; then
        printf "--from-url requires a value\n" >&2
        exit 1
      fi
      URL_BASE="$1"
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
  local)
    install_from_local || {
      printf "Could not install from local file ./lo\n" >&2
      exit 1
    }
    ;;
  url)
    if [ -z "$URL_BASE" ]; then
      printf "Missing URL base\n" >&2
      exit 1
    fi
    install_from_url "$URL_BASE"
    ;;
  auto)
    if install_from_local; then
      :
    else
      printf "Local file ./lo not found.\n" >&2
      printf "Use --from-url <raw-base-url> for remote install.\n" >&2
      exit 1
    fi
    ;;
esac

if command -v lo >/dev/null 2>&1; then
  printf "\nlo is available: %s\n" "$(command -v lo)"
else
  printf "\nInstall completed at %s\n" "$BIN_PATH"
fi

ensure_path_hint
printf "Run 'lo --help' to get started.\n"
