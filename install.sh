#!/usr/bin/env bash
# install.sh — installs qnap-led end to end:
#   - downloads the release binary (verifying its checksum)
#   - writes a default /etc/qnap-led/config.yaml, but ONLY if one doesn't
#     already exist — safe to re-run to upgrade without losing your config
#   - installs and enables the systemd service
#
# Usage (fetch straight from GitHub, no clone needed):
#   curl -fsSL https://raw.githubusercontent.com/arojas90/qnap-led/main/install.sh | sudo bash
#
# Or, after cloning the repo:
#   sudo ./install.sh
#
# Env vars:
#   QNAP_LED_VERSION   release tag to install, e.g. "v0.1.0" (default: latest)

set -euo pipefail

REPO="arojas90/qnap-led"
VERSION="${QNAP_LED_VERSION:-latest}"
BIN_PATH="/usr/local/bin/qnap-led"
CONFIG_DIR="/etc/qnap-led"
CONFIG_PATH="$CONFIG_DIR/config.yaml"
SERVICE_PATH="/etc/systemd/system/qnap-led.service"

if [ "$(id -u)" -ne 0 ]; then
  echo "error: run this as root, e.g. 'sudo ./install.sh'" >&2
  exit 1
fi

if ! command -v curl >/dev/null 2>&1; then
  echo "error: curl is required but not installed" >&2
  exit 1
fi

if [ "$VERSION" = "latest" ]; then
  BASE_URL="https://github.com/$REPO/releases/latest/download"
else
  BASE_URL="https://github.com/$REPO/releases/download/$VERSION"
fi

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

echo "==> Downloading qnap-led ($VERSION)"
curl -fsSL -o "$TMP_DIR/qnap-led" "$BASE_URL/qnap-led"
curl -fsSL -o "$TMP_DIR/qnap-led.sha256" "$BASE_URL/qnap-led.sha256"

echo "==> Verifying checksum"
(cd "$TMP_DIR" && sha256sum -c qnap-led.sha256)

echo "==> Stopping existing service (if any), so the binary isn't busy"
systemctl stop qnap-led.service 2>/dev/null || true

echo "==> Installing binary to $BIN_PATH"
install -m 0755 "$TMP_DIR/qnap-led" "$BIN_PATH"

echo "==> Ensuring $CONFIG_DIR exists"
mkdir -p "$CONFIG_DIR"

if [ -e "$CONFIG_PATH" ]; then
  echo "==> Existing config found at $CONFIG_PATH — leaving it untouched"
else
  echo "==> Writing default config to $CONFIG_PATH"
  cat >"$CONFIG_PATH" <<'CONFIG_EOF'
# qnap-led configuration.
# Full annotated reference: https://github.com/arojas90/qnap-led/blob/main/config.example.yaml
#
# Sections that depend on external configuration (mqtt, web) are disabled
# until you fill them in below — an empty/missing value means that feature
# stays off. After editing, restart the service:
#   sudo systemctl restart qnap-led

port: /dev/ttyS1
baud: 1200
pool: tank
os_disk_device: sdg
refresh: 5s
custom_screens_file: /etc/qnap-led/custom_screens.json

# mqtt:
#   broker: "tcp://localhost:1883"
#   username: "qnap-led"
#   password: "change-me"

# web:
#   addr: "127.0.0.1:8080"
CONFIG_EOF
fi

echo "==> Installing systemd service to $SERVICE_PATH"
cat >"$SERVICE_PATH" <<'SERVICE_EOF'
[Unit]
Description=QNAP front panel LCD/button daemon
After=zfs-mount.service network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/qnap-led
Restart=on-failure
RestartSec=5
User=root

[Install]
WantedBy=multi-user.target
SERVICE_EOF

echo "==> Enabling and starting qnap-led.service"
systemctl daemon-reload
systemctl enable --now qnap-led.service

cat <<EOF

Done.

  Config:  $CONFIG_PATH
  Binary:  $BIN_PATH
  Service: qnap-led.service

Edit the config (set 'pool', 'mqtt.broker', 'web.addr', etc.) then:
  sudo systemctl restart qnap-led

Watch it run:
  sudo journalctl -u qnap-led -f
EOF
