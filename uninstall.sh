#!/usr/bin/env bash
set -euo pipefail

BINARY_NAME="ananta"

if [ -n "${PREFIX:-}" ] && [[ "$PREFIX" == *"com.termux/files/usr"* ]]; then
    INSTALL_DIR="$PREFIX/bin"
    IS_TERMUX=1
else
    INSTALL_DIR="/usr/local/bin"
    IS_TERMUX=0
fi

SUDO=""
if [ "$IS_TERMUX" -eq 0 ] && [ "$EUID" -ne 0 ]; then
    if command -v sudo >/dev/null 2>&1; then
        SUDO="sudo"
    else
        echo "Error: Root privileges or sudo are required to remove files from ${INSTALL_DIR}."
        exit 1
    fi
fi

TARGET="${INSTALL_DIR}/${BINARY_NAME}"
if [ -f "$TARGET" ]; then
    echo "==> Removing ${TARGET}..."
    if [ -n "$SUDO" ]; then
        $SUDO rm -f "$TARGET"
    else
        rm -f "$TARGET"
    fi
    echo "==> Ananta uninstalled successfully."
else
    echo "Ananta is not found at ${TARGET}."
fi
