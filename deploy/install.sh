#!/usr/bin/env bash
# Installs kicker on Raspberry Pi OS Lite (64-bit) – or updates an installation by hand; the data
# stays (spec 10 §5). The SD card image is the same system, ready made.
#
#   curl -fsSL https://raw.githubusercontent.com/Humbarrt/kicker/main/deploy/install.sh | sudo bash
#   sudo ./install.sh [kicker-linux-arm64]     # a binary you built yourself (make build-pi)
#
# Without a binary it downloads the newest release and checks it against release.json; the
# signature of release.json is checked too if minisign is installed. The setup itself is
# `kicker install` (user, data folder, systemd unit, udev rules, host name).
set -euo pipefail

REPO=Humbarrt/kicker
[[ $EUID -eq 0 ]] || { echo "run it as root: sudo $0" >&2; exit 1; }
case $(uname -m) in
aarch64) arch=arm64 ;;
x86_64) arch=amd64 ;;
*) echo "unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

if [[ $# -ge 1 ]]; then
	bin=$1
else
	base=https://github.com/$REPO/releases/latest/download
	echo "downloading the newest release"
	curl -fsSL -o "$tmp/release.json" "$base/release.json"
	curl -fsSL -o "$tmp/release.json.minisig" "$base/release.json.minisig"
	curl -fsSL -o "$tmp/kicker-linux-$arch" "$base/kicker-linux-$arch"
	if command -v minisign >/dev/null; then
		curl -fsSL -o "$tmp/release.pub" "https://raw.githubusercontent.com/$REPO/main/deploy/release.pub"
		minisign -Vm "$tmp/release.json" -p "$tmp/release.pub"
	fi
	want=$(python3 -c 'import json,sys; m=json.load(open(sys.argv[1])); print(next(s["sha256"] for s in m["server"] if s["name"]==sys.argv[2]))' "$tmp/release.json" "kicker-linux-$arch")
	echo "$want  $tmp/kicker-linux-$arch" | sha256sum -c --quiet
	bin=$tmp/kicker-linux-$arch
fi
chmod +x "$bin"

# kicker.local over mDNS (Raspberry Pi OS Lite usually has it already)
command -v avahi-daemon >/dev/null || { apt-get update -qq && apt-get install -y -qq avahi-daemon; }

"$bin" install
