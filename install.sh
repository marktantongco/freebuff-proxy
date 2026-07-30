#!/usr/bin/env bash
set -euo pipefail

# ─── Configuration ──────────────────────────────────────────────────────
BINARY_NAME="freebuff-proxy"
INSTALL_DIR="/usr/local/bin"
SERVICE_DIR="/etc/systemd/system"
SOURCE_DIR="$(cd "$(dirname "$0")" && pwd)"
BUILD_DIR="${SOURCE_DIR}/cmd/freebuff-proxy"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info()  { echo -e "${GREEN}[✓]${NC} $1"; }
log_warn()  { echo -e "${YELLOW}[!]${NC} $1"; }
log_error() { echo -e "${RED}[✗]${NC} $1"; }

# ─── Check prerequisites ────────────────────────────────────────────────
if ! command -v go &>/dev/null; then
    log_error "Go is not installed. Install Go 1.25+ first: https://go.dev/dl/"
    exit 1
fi

if ! command -v systemctl &>/dev/null; then
    log_warn "systemctl not found — skipping systemd service setup."
    SKIP_SYSTEMD=true
else
    SKIP_SYSTEMD=false
fi

# ─── Build binary ───────────────────────────────────────────────────────
echo ""
log_info "Building ${BINARY_NAME} from ${BUILD_DIR}..."

cd "${SOURCE_DIR}"
go build -ldflags='-s -w' -o "/tmp/${BINARY_NAME}" "${BUILD_DIR}"

log_info "Binary built at /tmp/${BINARY_NAME}: $(ls -lh "/tmp/${BINARY_NAME}" | awk '{print $5}')"

# ─── Install binary ─────────────────────────────────────────────────────
echo ""
log_info "Installing ${BINARY_NAME} to ${INSTALL_DIR}..."

sudo cp "/tmp/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
sudo chmod 755 "${INSTALL_DIR}/${BINARY_NAME}"

log_info "Installed: $(which ${BINARY_NAME})"

# ─── Create config directory ────────────────────────────────────────────
echo ""
log_info "Creating config directory..."

CONFIG_DIR="$HOME/.config/manicode"
mkdir -p "${CONFIG_DIR}"
log_info "Config dir: ${CONFIG_DIR}"

# ─── Create systemd service ─────────────────────────────────────────────
if [ "${SKIP_SYSTEMD}" = false ]; then
    echo ""
    log_info "Installing systemd service..."

    SERVICE_FILE="${SERVICE_DIR}/${BINARY_NAME}.service"

    # Determine the user's home directory
    USER_HOME="$HOME"

    sudo tee "${SERVICE_FILE}" > /dev/null <<SERVICEEOF
[Unit]
Description=Freebuff Proxy — JA3 Stealth Gateway + SOCKS5 Proxy Pool
Documentation=https://github.com/ferdiunal/freebuff-proxy
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${INSTALL_DIR}/${BINARY_NAME} serve
Restart=on-failure
RestartSec=5

# Environment variables (override in .env or via EnvironmentFile)
EnvironmentFile=${CONFIG_DIR}/proxy.env
Environment=FREEBUFF_PROXY_ADDR=127.0.0.1:1455
Environment=STEALTH_ENABLED=false
Environment=DASHBOARD_ENABLED=true
Environment=DASHBOARD_ADDR=:9091

# Sandboxing
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=${CONFIG_DIR}
PrivateTmp=true
PrivateDevices=true

[Install]
WantedBy=multi-user.target
SERVICEEOF

    sudo systemctl daemon-reload
    log_info "Service file installed: ${SERVICE_FILE}"
    log_info ""
    log_info "To enable and start:"
    log_info "  sudo systemctl enable ${BINARY_NAME}"
    log_info "  sudo systemctl start ${BINARY_NAME}"
    log_info ""
    log_info "To view logs:"
    log_info "  sudo journalctl -u ${BINARY_NAME} -f"
fi

# ─── Create example .env ────────────────────────────────────────────────
echo ""
log_info "Creating example .env file..."

if [ ! -f "${CONFIG_DIR}/proxy.env" ]; then
    cat > "${CONFIG_DIR}/proxy.env" <<'ENVEOF'
# Freebuff Proxy Configuration
# Copy and uncomment the variables you need.

# Proxy listen address (default: 127.0.0.1:1455)
# FREEBUFF_PROXY_ADDR=127.0.0.1:1455

# API base URL (default: https://www.codebuff.com)
# FREEBUFF_API_BASE_URL=https://www.codebuff.com

# Default model (default: deepseek/deepseek-v4-pro)
# FREEBUFF_MODEL=deepseek/deepseek-v4-pro

# Freebuff credentials file path
# FREEBUFF_CREDENTIALS_PATH=$HOME/.config/manicode/credentials.json

# Multi-token auth rotation (comma-separated)
# AUTH_TOKENS=token1,token2,token3

# Proxy API key for authentication
# FREEBUFF_PROXY_API_KEY=sk-your-key-here

# ── JA3 Stealth Transport ─────────────────────────────────────────────
# STEALTH_ENABLED=true
# STEALTH_PROFILE=chrome120    # chrome120, safari17, firefox121, random

# ── SOCKS5 Proxy Rotation ──────────────────────────────────────────────
# PROXY_URL=https://proxy.webshare.io/api/proxy/list
# PROXY_REFRESH_MINS=30
# PROXY_STRICT_GEO=false
# PROXY_GEO_VERIFY=false

# ── Dashboard ──────────────────────────────────────────────────────────
# DASHBOARD_ENABLED=true
# DASHBOARD_ADDR=:9091
# DASHBOARD_PREFIX=/dashboard
ENVEOF
    log_info ".env template created: ${CONFIG_DIR}/proxy.env"
    log_warn "Edit ${CONFIG_DIR}/proxy.env with your actual credentials!"
else
    log_warn "${CONFIG_DIR}/proxy.env already exists — skipping"
fi

# ─── Verify installation ────────────────────────────────────────────────
echo ""
log_info "Verifying installation..."

if command -v "${BINARY_NAME}" &>/dev/null; then
    log_info "${BINARY_NAME} is available: $(which ${BINARY_NAME})"
    ls -lh "$(which ${BINARY_NAME})" | awk '{print "  Binary:", $NF, "(" $5 ")"}'
else
    log_error "${BINARY_NAME} not found in PATH!"
    exit 1
fi

# ─── Summary ────────────────────────────────────────────────────────────
echo ""
echo "═══════════════════════════════════════════════════════════════"
echo -e "  ${GREEN}Freebuff Proxy${NC} — Installation Complete"
echo "═══════════════════════════════════════════════════════════════"
echo ""
echo "  Binary:       ${INSTALL_DIR}/${BINARY_NAME}"
echo "  Config dir:   ${CONFIG_DIR}"
echo "  Config file:  ${CONFIG_DIR}/proxy.env"
echo ""
echo "  Quick start:"
echo "    ${BINARY_NAME} serve"
echo ""
echo "  Login:"
echo "    ${BINARY_NAME} login"
echo ""
echo "  Health check:"
echo "    curl http://127.0.0.1:1455/healthz"
echo ""

if [ "${SKIP_SYSTEMD}" = false ]; then
    echo "  Service management:"
    echo "    sudo systemctl enable ${BINARY_NAME}"
    echo "    sudo systemctl start ${BINARY_NAME}"
    echo "    sudo journalctl -u ${BINARY_NAME} -f"
fi

echo "═══════════════════════════════════════════════════════════════"
