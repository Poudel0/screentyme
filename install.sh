#!/usr/bin/env bash
set -euo pipefail

REPO="Poudel0/screentyme"
BIN_NAME="screentyme"
INSTALL_DIR="${HOME}/.local/bin"
SERVICE_DIR="${HOME}/.config/systemd/user"

# Detect architecture
case "$(uname -m)" in
  x86_64)        ARCH="x86_64" ;;
  aarch64|arm64) ARCH="aarch64" ;;
  *) echo "error: unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

# Resolve latest release tag
echo "Fetching latest release..."
TAG=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep '"tag_name"' \
  | sed 's/.*"tag_name": *"\(.*\)".*/\1/')

VERSION="${TAG#v}"
TARBALL="${BIN_NAME}_${VERSION}_linux_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${TAG}/${TARBALL}"

# Download and install binary
echo "Downloading ${BIN_NAME} ${TAG}..."
TMP=$(mktemp -d)
trap 'rm -rf "${TMP}"' EXIT

curl -fsSL "${URL}" -o "${TMP}/${TARBALL}"
tar -xzf "${TMP}/${TARBALL}" -C "${TMP}"

mkdir -p "${INSTALL_DIR}"
install -m755 "${TMP}/${BIN_NAME}" "${INSTALL_DIR}/${BIN_NAME}"
echo "Installed: ${INSTALL_DIR}/${BIN_NAME}"

# Warn if ~/.local/bin is not in PATH
if ! echo "${PATH}" | grep -q "${HOME}/.local/bin"; then
  echo "warning: ${HOME}/.local/bin is not in your PATH"
  echo "  Add this to your shell profile: export PATH=\"\${HOME}/.local/bin:\${PATH}\""
fi

# Write systemd user service
mkdir -p "${SERVICE_DIR}"
cat > "${SERVICE_DIR}/${BIN_NAME}.service" <<EOF
[Unit]
Description=Screentyme screentime tracking daemon
After=graphical-session.target

[Service]
Type=simple
ExecStart=${INSTALL_DIR}/${BIN_NAME}
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
EOF

# Enable linger so the service survives log-out (harmless if already set)
loginctl enable-linger "$(id -un)" 2>/dev/null || true

systemctl --user daemon-reload
systemctl --user enable --now "${BIN_NAME}"

echo ""
echo "Screentyme is running."
echo "  Web UI:  http://127.0.0.1:7777"
echo "  Status:  systemctl --user status screentyme"
echo "  Logs:    journalctl --user -u screentyme -f"
