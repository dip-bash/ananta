#!/usr/bin/env bash
set -e

REPO="dip-bash/ananta"
BINARY_NAME="ananta"
INSTALL_DIR="/usr/local/bin"

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64)
        BIN_FILE="ananta-linux-x86_64"
        ;;
    aarch64|arm64)
        BIN_FILE="ananta-linux-aarch64"
        ;;
    *)
        echo "Error: Unsupported architecture ($ARCH). Ananta supports x86_64 and aarch64."
        exit 1
        ;;
esac

# Check for root / sudo privileges
if [ "$EUID" -ne 0 ]; then
    if command -v sudo >/dev/null 2>&1; then
        SUDO="sudo"
    else
        echo "Error: This script requires root privileges to install into ${INSTALL_DIR}. Please run as root or install sudo."
        exit 1
    fi
else
    SUDO=""
fi

URL="https://github.com/${REPO}/releases/latest/download/${BIN_FILE}"

echo "==> Downloading Ananta for ${ARCH}..."
TMP_FILE=$(mktemp)
if ! curl -sSL "$URL" -o "$TMP_FILE"; then
    echo "Error: Failed to download binary from $URL"
    rm -f "$TMP_FILE"
    exit 1
fi

echo "==> Installing to ${INSTALL_DIR}/${BINARY_NAME}..."
$SUDO install -m 755 "$TMP_FILE" "${INSTALL_DIR}/${BINARY_NAME}"
rm -f "$TMP_FILE"

echo "==> Ananta installed successfully!"
echo "Run 'ananta --init' to generate your initial configuration."
