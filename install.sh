#!/bin/sh
set -e

# Waveloom installer — detects OS/arch, downloads latest release, installs to ~/.local/bin
# Usage: curl -fsSL https://raw.githubusercontent.com/Menfre01/waveloom/main/install.sh | sh

REPO="Menfre01/waveloom"
INSTALL_DIR="${HOME}/.local/bin"
BINARY="waveloom"

# Detect OS
case "$(uname -s)" in
    Darwin)  OS="darwin" ;;
    Linux)   OS="linux" ;;
    *)       echo "Unsupported OS: $(uname -s)" >&2; exit 1 ;;
esac

# Detect architecture
case "$(uname -m)" in
    x86_64|amd64)  ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *)             echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

# Fetch latest release tag
LATEST_TAG=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
if [ -z "$LATEST_TAG" ]; then
    echo "Failed to determine latest release. Trying default..." >&2
    LATEST_TAG="v0.1.0-alpha.6"
fi

DOWNLOAD_URL="https://github.com/${REPO}/releases/latest/download/${BINARY}_${OS}_${ARCH}.tar.gz"

echo "→ Downloading Waveloom ${LATEST_TAG} (${OS}/${ARCH})..."
echo "  ${DOWNLOAD_URL}"

# Download and extract
mkdir -p "${INSTALL_DIR}"
TMP_DIR=$(mktemp -d)
curl -fsSL "${DOWNLOAD_URL}" | tar -xz -C "${TMP_DIR}" "${BINARY}"
mv "${TMP_DIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
chmod +x "${INSTALL_DIR}/${BINARY}"
rm -rf "${TMP_DIR}"

echo ""
echo "✓ Waveloom ${LATEST_TAG} installed to ${INSTALL_DIR}/${BINARY}"

# Check PATH
if ! echo "${PATH}" | grep -q "${INSTALL_DIR}"; then
    echo ""
    echo "⚠  ${INSTALL_DIR} is not in your PATH."
    echo "   Add the following to your ~/.bashrc or ~/.zshrc:"
    echo ""
    echo "   export PATH=\"\$HOME/.local/bin:\$PATH\""
fi

echo ""
echo "Next steps:"
echo "  waveloom setup    # Configure your DeepSeek API Key"
echo "  waveloom          # Launch the TUI"
