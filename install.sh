#!/usr/bin/env bash
set -euo pipefail

REPO="dip-bash/ananta"
BINARY_NAME="ananta"

# Termux sets PREFIX to its private user-space prefix. Do not use sudo there.
if [ -n "${PREFIX:-}" ] && [[ "$PREFIX" == *"com.termux/files/usr"* ]]; then
    INSTALL_DIR="$PREFIX/bin"
    IS_TERMUX=1
else
    INSTALL_DIR="/usr/local/bin"
    IS_TERMUX=0
fi

ARCH=$(uname -m)
if [ "$IS_TERMUX" -eq 1 ]; then
    case "$ARCH" in
        aarch64|arm64)
            BIN_FILE="ananta-android-aarch64"
            ;;
        *)
            echo "Error: Unsupported Termux architecture ($ARCH). Ananta currently supports Android ARM64 (aarch64) only."
            exit 1
            ;;
    esac
else
    case "$ARCH" in
        x86_64|amd64)
            BIN_FILE="ananta-linux-x86_64"
            ;;
        aarch64|arm64)
            BIN_FILE="ananta-linux-aarch64"
            ;;
        *)
            echo "Error: Unsupported Linux architecture ($ARCH). Ananta supports x86_64 and aarch64."
            exit 1
            ;;
    esac
fi

SUDO=""
if [ "$IS_TERMUX" -eq 0 ] && [ "$EUID" -ne 0 ]; then
    if command -v sudo >/dev/null 2>&1; then
        SUDO="sudo"
    else
        echo "Error: Root privileges or sudo are required to install into ${INSTALL_DIR}."
        exit 1
    fi
fi

URL="https://github.com/${REPO}/releases/latest/download/${BIN_FILE}"
echo "==> Downloading Ananta ($BIN_FILE)..."
TMP_FILE=$(mktemp)
trap 'rm -f "$TMP_FILE"' EXIT

if ! curl --fail --location --progress-bar "$URL" --output "$TMP_FILE"; then
    echo "Error: Failed to download binary from $URL"
    exit 1
fi

if [ ! -s "$TMP_FILE" ]; then
    echo "Error: Downloaded file is empty."
    exit 1
fi

echo "==> Installing to ${INSTALL_DIR}/${BINARY_NAME}..."
if [ -n "$SUDO" ]; then
    $SUDO install -m 755 "$TMP_FILE" "${INSTALL_DIR}/${BINARY_NAME}"
else
    install -m 755 "$TMP_FILE" "${INSTALL_DIR}/${BINARY_NAME}"
fi

echo "==> Ananta installed successfully!"
echo "Run 'ananta --init' to generate your initial configuration."
