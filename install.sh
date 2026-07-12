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

DOWNLOAD_URL="https://github.com/${REPO}/releases/latest/download/${BINARY}_${OS}_${ARCH}.tar.gz"

echo "→ Downloading Waveloom (${OS}/${ARCH})..."
echo "  ${DOWNLOAD_URL}"

# Download and extract
mkdir -p "${INSTALL_DIR}"
TMP_DIR=$(mktemp -d)
curl -fsSL "${DOWNLOAD_URL}" | tar -xz -C "${TMP_DIR}" "${BINARY}"
mv "${TMP_DIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
chmod +x "${INSTALL_DIR}/${BINARY}"
rm -rf "${TMP_DIR}"

VERSION=$("${INSTALL_DIR}/${BINARY}" --version 2>/dev/null || echo "unknown")
echo ""
echo "✓ Waveloom ${VERSION} installed to ${INSTALL_DIR}/${BINARY}"

# Check PATH
if ! echo "${PATH}" | grep -q "${INSTALL_DIR}"; then
    SHELL_RC=""
    case "$(basename "${SHELL:-}")" in
        zsh)  SHELL_RC="${HOME}/.zshrc" ;;
        bash) SHELL_RC="${HOME}/.bashrc" ;;
        *)    SHELL_RC="${HOME}/.profile" ;;
    esac
    echo ""
    echo "→ Adding ${INSTALL_DIR} to PATH in ${SHELL_RC}..."
    echo "export PATH=\"\$HOME/.local/bin:\$PATH\"" >> "${SHELL_RC}"
    echo "  Run 'source ${SHELL_RC}' or restart your shell to apply."
fi

echo ""
echo "Next steps:"
echo "  waveloom setup    # Configure your DeepSeek API Key"
echo "  waveloom          # Launch the TUI"
