#!/usr/bin/env bash
# One-time setup of a Raspberry Pi 4 (Raspberry Pi OS Lite 64-bit) for kicker (spec 04 §6).
# Usage on the Pi:  sudo ./install.sh /path/to/kicker-linux-arm64
set -euo pipefail

BIN="${1:?usage: sudo $0 /path/to/kicker-linux-arm64}"
[[ $EUID -eq 0 ]] || { echo "run as root (sudo)"; exit 1; }

id kicker &>/dev/null || useradd --system --home /var/lib/kicker --shell /usr/sbin/nologin kicker
usermod -aG dialout kicker

install -d -o kicker -g kicker -m 0750 /var/lib/kicker
install -d -m 0755 /opt/kicker
[[ -f /opt/kicker/kicker ]] && mv /opt/kicker/kicker /opt/kicker/kicker.prev
install -m 0755 "$BIN" /opt/kicker/kicker

install -m 0644 "$(dirname "$0")/kicker.service" /etc/systemd/system/kicker.service

# mDNS host name kicker.local (spec 03 §9)
apt-get install -y avahi-daemon >/dev/null
hostnamectl set-hostname kicker

systemctl daemon-reload
systemctl enable --now kicker
echo "kicker is running: https://kicker.local (first visit: http://kicker.local/trust)"
