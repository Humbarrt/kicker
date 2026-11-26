#!/usr/bin/env bash
# Builds the SD card image (spec 10 §5, decision D): the official Raspberry Pi OS Lite (64-bit)
# with kicker set up by `kicker install --root` from an x86 build – nothing runs inside the
# image, so no emulation is needed. Needs root (loop devices). CI runs it for every release.
#
#   sudo deploy/image/build.sh VERSION KICKER_AMD64 KICKER_ARM64 OUT_DIR
#
# PIOS_URL picks another Raspberry Pi OS image (default: the newest Lite arm64); PIOS_IMAGE uses
# an image file that is already here (.img or .img.xz, for testing).
set -euo pipefail

version=${1:?version}
tool=$(realpath "${2:?the kicker binary that runs here}")
arm=$(realpath "${3:?the kicker binary for the Pi}")
out=$(realpath -m "${4:?output folder}")
[[ $EUID -eq 0 ]] || { echo "run as root (loop devices)"; exit 1; }

work=$(mktemp -d)
mnt=$work/root
loop=""
bootloop=""
cleanup() {
	set +e
	mountpoint -q "$mnt/boot/firmware" && umount "$mnt/boot/firmware"
	mountpoint -q "$mnt" && umount "$mnt"
	[[ -n $loop ]] && losetup -d "$loop"
	[[ -n $bootloop ]] && losetup -d "$bootloop"
	rm -rf "$work"
}
trap cleanup EXIT

# 1. Raspberry Pi OS Lite, checked against its published sha256
if [[ -n ${PIOS_IMAGE:-} ]]; then
	src=$PIOS_IMAGE
	echo "using $src"
else
	url=$(curl -fsSIL -o /dev/null -w '%{url_effective}' "${PIOS_URL:-https://downloads.raspberrypi.com/raspios_lite_arm64_latest}")
	echo "downloading $url"
	curl -fsSL -o "$work/$(basename "$url")" "$url"
	curl -fsSL -o "$work/sha256" "$url.sha256"
	(cd "$work" && sha256sum -c sha256)
	src=$work/$(basename "$url")
	basename "$url" > "$work/base"
fi
img=$work/kicker.img
case $src in
*.xz) xz -dc "$src" > "$img" ;;
*) cp "$src" "$img" ;;
esac

# 2. mount its root and boot partitions (by their offsets: works without udev, e.g. in a
#    container, where partition devices don't appear)
part() { # number → "offset size" in bytes
	sfdisk --json "$img" | python3 -c "import json,sys; p=json.load(sys.stdin)['partitiontable']; s=p.get('sectorsize',512); x=p['partitions'][$1-1]; print(x['start']*s, x['size']*s)"
}
read -r off size < <(part 2)
loop=$(losetup --find --show --offset "$off" --sizelimit "$size" "$img")
read -r boff bsize < <(part 1)
bootloop=$(losetup --find --show --offset "$boff" --sizelimit "$bsize" "$img")
mkdir -p "$mnt"
mount "$loop" "$mnt"
mkdir -p "$mnt/boot/firmware"
mount "$bootloop" "$mnt/boot/firmware"

# 3. kicker: user, data folder with the binary, unit, udev rules, host name
"$tool" install --root "$mnt" --binary "$arm" --version "$version" --hostname kicker --no-start

# 4. done: unmount, compress, checksum
sync
umount "$mnt/boot/firmware" "$mnt"
losetup -d "$loop" "$bootloop"
loop=""
bootloop=""
mkdir -p "$out"
name=kicker-$version-rpi-arm64.img.xz
xz -T0 -6 -c "$img" > "$out/$name"
(cd "$out" && sha256sum "$name" > "$name.sha256")
echo "$out/$name"
[[ -f $work/base ]] && echo "based on $(cat "$work/base")"
true
