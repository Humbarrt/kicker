# Installing kicker on a Raspberry Pi

kicker runs on a Raspberry Pi 4 or 5 with Raspberry Pi OS Lite (64-bit). There are two ways:
the ready-made SD card image, or the install script on a Raspberry Pi OS you set up yourself.
Both give the same system:

- the `kicker` service (systemd), started at boot, answering on `http://kicker.local`
  and `https://kicker.local`
- the data folder `/var/lib/kicker` – database, pictures, certificates, backups – and the
  installed versions in `/var/lib/kicker/bin` (`current` is the running one; updates switch it
  and switch back if a new version fails)
- USB drives with FAT, exFAT or NTFS are mounted at `/media/kicker/<label>` for backups
- modules in their bootloader can be flashed by the service (firmware updates)

## The SD card image

1. Download `kicker-<version>-rpi-arm64.img.xz` from the
   [newest release](https://github.com/Humbarrt/kicker/releases/latest).
2. In [Raspberry Pi Imager](https://www.raspberrypi.com/software/): *Choose OS* → *Use custom*
   → the downloaded file; choose the SD card.
3. *Edit settings*:
   - host name: `kicker` (or another name: the kicker is then `<name>.local`)
   - a user with a password – for SSH or a keyboard, kicker doesn't use it
   - Wi-Fi, if the Pi isn't on a cable
   - SSH: only if you want to log in on the Pi; nothing else listens on the network
4. Write the card, put it into the Pi, power it on. After a minute or two – the first start
   enlarges the file system – open `http://kicker.local` and follow the setup wizard.

## The install script

On a Raspberry Pi OS Lite (64-bit), logged in on the Pi:

```sh
curl -fsSL https://raw.githubusercontent.com/Humbarrt/kicker/main/deploy/install.sh | sudo bash
```

It downloads the newest release, checks it against its `release.json` (and the signature, if
`minisign` is installed), installs `avahi-daemon` if missing and runs `kicker install`. The host
name becomes `kicker` if it was still `raspberrypi`; otherwise it stays.

With a binary you built yourself (`make build-pi`): copy it to the Pi and run
`sudo ./kicker-linux-arm64 install` – or `make deploy PI=pi@kicker.local`.

Running it again installs another version by hand; the data stays. A Pi set up before
version 0.6 (the binary in `/opt/kicker`) is moved to the new layout the same way.

## Updates

Settings → Updates checks for new versions daily and installs them with a click (or by itself at
the backup time, if switched on). From a shell: `sudo -u kicker /var/lib/kicker/bin/current update`.
