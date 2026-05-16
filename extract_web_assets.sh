#!/bin/bash

# Configuration
DOWNLOAD_URL="http://xmsecu.com:8080/ocx/VideoPlayToolSetup.exe"
TEMP_EXE="VideoPlayToolSetup.exe"

set -euo pipefail

echo "[*] xmcam-local: Web Assets Extraction Tool"

# 1. Check for 7z/7zz
if command -v 7zz >/dev/null 2>&1; then
    ZIP_CMD="7zz"
elif command -v 7z >/dev/null 2>&1; then
    ZIP_CMD="7z"
else
    echo "[!] Error: 7-Zip (7zz or 7z) is not installed."
    echo "    On macOS: brew install sevenzip"
    echo "    On Ubuntu/Debian: sudo apt install p7zip-full"
    exit 1
fi

# 2. Download
if [ ! -f "$TEMP_EXE" ]; then
    echo "[*] Downloading official VideoPlayTool installer..."
    curl -L -o "$TEMP_EXE" "$DOWNLOAD_URL"
else
    echo "[*] Installer already exists, skipping download."
fi

# 3. Extract
echo "[*] Extracting Web/ directory..."
$ZIP_CMD x "$TEMP_EXE" "Web/" -aoa

# 4. Cleanup
echo "[*] Cleaning up temporary files..."
rm "$TEMP_EXE"

echo "[SUCCESS] Web assets are ready in the ./Web folder."
echo "[*] You can now run: go run main.go"
