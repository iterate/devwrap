#!/usr/bin/env bash
set -euo pipefail

BIN_NAME="devwrap"
PREFIX="${PREFIX:-/usr/local}"
BIN_DIR="${BIN_DIR:-$PREFIX/bin}"
DEST="$BIN_DIR/$BIN_NAME"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

usage() {
  cat <<'EOF'
Usage: ./install.sh [-v VERSION]

Installs devwrap from GitHub releases.

Arguments:
  -v, --version VERSION
            Optional release version (with or without leading "v").
            Omit to install the latest release.

Environment:
  PREFIX    Install prefix (default: /usr/local)
  BIN_DIR   Install bin dir (default: $PREFIX/bin)
EOF
}

VERSION_INPUT=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help)
      usage
      exit 0
      ;;
    -v|--version)
      if [[ $# -lt 2 ]]; then
        echo "error: $1 requires a value" >&2
        usage
        exit 1
      fi
      VERSION_INPUT="$2"
      shift 2
      ;;
    --version=*)
      VERSION_INPUT="${1#*=}"
      shift
      ;;
    -* )
      echo "error: unknown option: $1" >&2
      usage
      exit 1
      ;;
    *)
      if [[ -n "$VERSION_INPUT" ]]; then
        echo "error: version provided more than once" >&2
        usage
        exit 1
      fi
      VERSION_INPUT="$1"
      shift
      ;;
  esac
done

detect_platform() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"

  case "$os" in
    darwin|linux) ;;
    *)
      echo "error: unsupported OS '$os'" >&2
      exit 1
      ;;
  esac

  case "$arch" in
    x86_64|amd64) arch="amd64" ;;
    arm64|aarch64) arch="arm64" ;;
    *)
      echo "error: unsupported architecture '$arch'" >&2
      exit 1
      ;;
  esac

  echo "${os}_${arch}"
}

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "error: required command not found: $1" >&2
    exit 1
  fi
}

need_cmd curl
need_cmd tar
need_cmd shasum

REPO="iterate/devwrap"
PLATFORM="$(detect_platform)"
ASSET="${BIN_NAME}_${PLATFORM}.tar.gz"
if [[ -z "$VERSION_INPUT" ]]; then
  echo "Resolving latest release for ${REPO}..."
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)"
  if [[ -z "$VERSION" ]]; then
    echo "error: failed to resolve latest release tag for ${REPO}" >&2
    exit 1
  fi
else
  VERSION="${VERSION_INPUT#v}"
  VERSION="v${VERSION}"
fi

BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
ARCHIVE_PATH="${TMP_DIR}/${ASSET}"
CHECKSUMS_PATH="${TMP_DIR}/checksums.txt"

echo "Downloading ${ASSET} from ${VERSION}..."
curl -fL "${BASE_URL}/${ASSET}" -o "$ARCHIVE_PATH"
curl -fL "${BASE_URL}/checksums.txt" -o "$CHECKSUMS_PATH"

echo "Verifying checksum..."
expected="$(grep "  ${ASSET}$" "$CHECKSUMS_PATH" | awk '{print $1}')"
if [[ -z "$expected" ]]; then
  echo "error: checksum entry for ${ASSET} not found" >&2
  exit 1
fi
actual="$(shasum -a 256 "$ARCHIVE_PATH" | awk '{print $1}')"
if [[ "$expected" != "$actual" ]]; then
  echo "error: checksum mismatch for ${ASSET}" >&2
  exit 1
fi

echo "Extracting..."
tar -xzf "$ARCHIVE_PATH" -C "$TMP_DIR"

EXTRACTED_BIN="${TMP_DIR}/${BIN_NAME}_${PLATFORM}"
if [[ ! -f "$EXTRACTED_BIN" ]]; then
  echo "error: archive did not contain expected binary '${BIN_NAME}_${PLATFORM}'" >&2
  exit 1
fi

echo "Installing to $DEST..."
if [[ ! -d "$BIN_DIR" ]]; then
  if [[ -w "$(dirname "$BIN_DIR")" ]]; then
    mkdir -p "$BIN_DIR"
  else
    sudo mkdir -p "$BIN_DIR"
  fi
fi

if [[ -w "$BIN_DIR" ]]; then
  install -m 0755 "$EXTRACTED_BIN" "$DEST"
else
  sudo install -m 0755 "$EXTRACTED_BIN" "$DEST"
fi

echo "Installed: $DEST"
echo "Version: $VERSION"
echo "Run: $BIN_NAME proxy status"
