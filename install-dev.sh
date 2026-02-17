#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SOURCE_DIR="$SCRIPT_DIR/cmd/devwrap"
BIN_NAME="devwrap"
PREFIX="${PREFIX:-/usr/local}"
BIN_DIR="${BIN_DIR:-$PREFIX/bin}"
DEST="$BIN_DIR/$BIN_NAME"

if [[ -n "${GO_BIN:-}" && -x "$GO_BIN" ]]; then
  GO_BIN="$GO_BIN"
elif [[ -x "/usr/local/go/bin/go" ]]; then
  GO_BIN="/usr/local/go/bin/go"
elif command -v go >/dev/null 2>&1; then
  GO_BIN="$(command -v go)"
else
  echo "error: go not found (set GO_BIN or install at /usr/local/go/bin/go)" >&2
  exit 1
fi

echo "Using Go: $GO_BIN"
echo "Building $BIN_NAME..."
"$GO_BIN" build -o "$SCRIPT_DIR/$BIN_NAME" "$SOURCE_DIR"

echo "Installing to $DEST..."
if [[ ! -d "$BIN_DIR" ]]; then
  if [[ -w "$(dirname "$BIN_DIR")" ]]; then
    mkdir -p "$BIN_DIR"
  else
    sudo mkdir -p "$BIN_DIR"
  fi
fi

if [[ -w "$BIN_DIR" ]]; then
  install -m 0755 "$SCRIPT_DIR/$BIN_NAME" "$DEST"
else
  sudo install -m 0755 "$SCRIPT_DIR/$BIN_NAME" "$DEST"
fi

echo "Installed: $DEST"
echo "Run: $BIN_NAME proxy status"
