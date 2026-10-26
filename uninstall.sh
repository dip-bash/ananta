#!/usr/bin/env bash
set -e

INSTALL_DIR="/usr/local/bin"
BINARY_NAME="ananta"

# Check for root / sudo privileges
if [ "$EUID" -ne 0 ]; then
    if command -v sudo >/dev/null 2>&1; then
        SUDO="sudo"
    else
        echo "Error: This script requires root privileges to remove files from ${INSTALL_DIR}."
        exit 1
    fi
else
    SUDO=""
fi

if [ -f "${INSTALL_DIR}/${BINARY_NAME}" ]; then
    echo "==> Removing ${INSTALL_DIR}/${BINARY_NAME}..."
    $SUDO rm -f "${INSTALL_DIR}/${BINARY_NAME}"
    echo "==> Ananta uninstalled successfully."
else
    echo "Ananta is not found in ${INSTALL_DIR}/${BINARY_NAME}."
fi
