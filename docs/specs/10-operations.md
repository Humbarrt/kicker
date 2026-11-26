# Spec 10: Operations (M7)

Status: **Approved v1** (2026-11-25, decisions in §9). Progress: tasks 1–4 ✓. Builds on D14 (backups), D15 (updates), D18 + spec 03 §8 and §12 (emergency kit, onboarding), spec 01 §9 (module updates) and spec 04 §5–6 (releases, deployment).

## 1. Scope
- The full onboarding wizard (spec 03 §12, steps 1–9).
- Encrypted backups: scheduled, on demand, download, restore (settings and onboarding), changing the passphrase.
- Automatic backup before database migrations and before updates.
- Signed updates of the server, with health check and rollback.
- Module firmware updates over USB (UF2).
- A ready-made SD card image.

Not in scope: network shares built into the server (SMB/NFS clients), WebDAV/S3 (D14: later), ESP firmware (`esptool`) updates – no ESP module exists yet.

## 2. Backups
### Content and format (D14, spec 03 §8)
- `kicker-backup-<name>-<YYYY-MM-DD-HHMMSS>.tar.gz.age`: a tar.gz, encrypted with age (scrypt, the backup passphrase).
- Inside:
  - `manifest.json`: format version, kicker name, `server_id`, server version, schema version, created at, file list with sha256
  - `kicker.db`: a `VACUUM INTO` snapshot (consistent, taken while the server runs)
  - `media/`: profile pictures
  - `identity/`: `ca.key`, `ca.crt` (the server certificate is not needed: it is reissued from the CA)
  - `backup.passphrase` (so scheduled backups keep running after a restore)
- A backup never leaves the Pi unencrypted, also not as a download.

### Backup cycle (owner, 2026-11-26: like Home Assistant's)
- **Schedule:** daily (default), on chosen weekdays, or only by hand.
- **Time:** automatic (default) – a fixed time between 03:00 and 05:00 picked from the kicker's id, so kickers on one network don't all back up at once – or a set time. If a game is on then, the backup waits until the table is free.
- **Retention** of automatic backups: the newest N (default 7), or the ones younger than N days. Backups made by hand, uploaded ones and the ones before updates and migrations stay until deleted (the latter: the last 3 each).
- **Back up now** makes a manual backup. The page shows the last successful backup and the next scheduled one.
- **Before a database migration** (in `store.migrate`): if an existing database has pending migrations, a backup goes to `<data>/backups/pre-migration/` first. No passphrase yet (before onboarding) means no data worth saving: skipped.
- **Before an update** (§3): to `<data>/backups/pre-update/`.
- File name: `kicker-backup-<name>-<YYYY-MM-DD-HHMMSS>-<kind>.tar.gz.age`, kind `auto`, `manual`, `pre-update`, `pre-migration` or `uploaded`: the list shows a backup's date and kind without opening it (opening one takes a second or more – the passphrase is deliberately slow to try).

### Locations (owner, 2026-11-25: several at once)
- Every backup is written to each **enabled location**. A location is a folder:
  - **This kicker:** `<data>/backups` – always there, can be switched off.
  - **USB drive:** a drive under `/media/kicker/<label>` (mounted by the image's udev rule, §5). Not plugged in at backup time: skipped and reported.
  - **Folder or network share:** a path the admin enters, e.g. an SMB/NFS share mounted by the OS (`/etc/fstab`); the docs explain how. The server only writes files.
- Per location: name, path, on/off (a switch), its last result. Admins get a badge when a location failed or – with a schedule – has no backup younger than 48 h (weekdays: 8 days).

### My backups
- A list of the backups in all enabled locations (plus the kicker's own pre-update, pre-migration and uploaded ones): created, size, kind, the locations it is in; filters by kind. A backup of another kicker (by its name) is marked.
- Per backup: **restore**, **download** (from each location), **delete** (from all locations).
- **Restore from the list tries the current passphrase**: a backup made since the last passphrase change restores without asking. Otherwise the dialog asks for the passphrase that was valid back then.
- **Upload a backup:** it joins the list (kept in `<data>/backups/uploads`), and the restore dialog opens right away – with the same rule: the current passphrase first, then asking.

### Restore
- From the list or an upload (settings), or upload + passphrase (onboarding step 2). Wrong passphrase, a damaged file, a backup of a newer schema than the server: reported **before anything is replaced**.
- The upload is unpacked and checked into `<data>/restore/` (sha256 of every file, `PRAGMA integrity_check`), then the server **restarts itself** (re-exec) and swaps the files in before opening the database: the current state goes to `<data>/backups/before-restore/` (unencrypted is fine: it doesn't leave the Pi), then the restored files take its place, then migrations run as usual.
- A restore brings back everything: identity, accounts, games, settings. Everybody logs in again (sessions are not restored).

### Passphrase
- Settings → Security: show the emergency kit (exists), **change the passphrase** (8 new words, or own ≥ 16 characters). Only new backups use it; the UI warns that older backups need the old one and offers "Back up now".

## 3. Server updates (D15)
### Releases
- A tag `vX.Y.Z` builds the release (spec 04 §5, `.github/workflows/release.yml`, `docs/releasing.md`) with **one signed file**: `release.json` + `release.json.minisig`.
  - `release.json` (format 1): `version`, `date`, `notes` (markdown, from the tag message), `min_version` (optional: older kickers update to an intermediate release first), `server[]`: `{name, url, sha256, size, os, arch}` for `kicker-linux-arm64` and `kicker-linux-amd64`, `firmware[]`: `{name, url, sha256, size, mf, hw, fw, fw_ver, upd}` – one `.uf2` per module and board, named `<fw>-<fw_ver>-<hw>.uf2`.
  - A module's identity comes from its `platformio.ini`: `[kicker] manifest` and `version` (passed to the firmware as build flags, so the release and the announce never differ), and `board` of each `[env:…]` is its `hw`.
  - Only `release.json` is signed (minisign, Ed25519, prehashed like the minisign tool; `aead.dev/minisign`); the trusted comment names the version. The public key is `deploy/release.pub` in the repo and is built into release binaries as `version.UpdatePubKey`; the private key, encrypted with a password, is in the repo secrets. Every other file is checked by its sha256 from `release.json`.
  - CI checks the new signature with the key built into the new binary before publishing. Tags with a pre-release part (`v1.2.0-rc.1`) become GitHub pre-releases.
  - `kicker release keygen | manifest | sign | verify` do each step by hand; `keygen --test` makes a key without a password for local test releases.
- **Check:** daily (if enabled in onboarding step 8 / settings) and "Check now": the newest release of `version.UpdateRepo` via the GitHub API. The repo is public (owner, 2026-11-26), so the check and the downloads need no token. Unauthenticated GitHub API calls are limited to 60 per hour per IP address: one check a day plus "Check now" stays far below that; a `403`/`429` is shown as "GitHub is busy, try again later".
- **Install automatically** (setting, default off): an available server update is installed at the backup time when the table is idle. **Off: admins get the same alert as for firmware** – a badge and "Update to X.Y.Z" with the notes – and install it by hand.

### Install
1. Refused while a game or a tournament game is being played (the button says why).
2. Backup to `pre-update/`.
3. Download the server binary, check sha256 against the verified `release.json`.
4. Install it as `<data>/bin/kicker-X.Y.Z`; `<data>/bin/current` and `<data>/bin/previous` are symlinks. systemd runs `<data>/bin/current` (the install script and the image set this up).
5. Write `<data>/update.json` (from, to, pre-update backup, attempt 0), switch `current`, **re-exec** the new binary (same process id, systemd keeps watching).
6. **Health check:** the new binary is healthy when it has opened and migrated the database, the HTTP(S) servers answer and the table actor runs, and it stays up for 60 s. Then it deletes `update.json` and keeps only `current` and `previous`.
7. **Rollback:** if the new binary exits or crashes before it is healthy, systemd starts `current` again; it finds `update.json` with an attempt already used and rolls back: `current` → the previous binary, **and the pre-update backup is restored** (the old binary refuses a migrated database). At most a few minutes of play are lost. Admins see "Update to X.Y.Z failed, rolled back" with the log lines.
- `kicker update` (CLI) does the same from a shell, `kicker update --rollback` goes back by hand.
- Development builds and forks without `UpdateRepo` show "Updates are disabled in this build".

## 4. Module firmware updates (spec 01 §9)
- `release.json` lists the firmware files. A module whose `mf` and `hw` match an entry with a newer version than its `fw.ver` shows "Firmware X.Y.Z available" on the modules page, and admins get a badge; "Update all" updates them one after the other. **Always by hand** (owner, 2026-11-25): a module update makes the table unusable for a moment.
- Only while the table is idle or asleep (never during a game).
- **Flow (`upd: uf2`):**
  1. Download + sha256 check.
  2. Send `update`; the module acks and reboots into the RP2040/RP2350 bootloader.
  3. The bootloader appears as a USB drive (`2e8a:0003` RP2040, `2e8a:000f` RP2350). The server writes the `.uf2` blocks to it (§9, decision C).
  4. The module reboots and announces the new `fw.ver` within 30 s → done. Otherwise: "Update failed" with the log; a module stuck in the bootloader is shown as such and can be flashed again (it needs no firmware to accept a `.uf2`).
- A module can also be updated with an uploaded `.uf2` (own builds), admins only, with a warning.

## 5. SD card image
- A release also publishes `kicker-<version>-rpi4-arm64.img.xz`: Raspberry Pi OS Lite 64-bit with kicker installed (§9, decision D, how it is built).
- What the image adds:
  - the `kicker` user, `/var/lib/kicker` with `bin/current`, the systemd unit, host name `kicker`, avahi
  - a udev rule: USB drives mount to `/media/kicker/<label>` writable by `kicker` (backups); the RP2 bootloader drive is accessible to `kicker` (firmware updates)
  - nothing else listens on the network; SSH stays off unless enabled in Raspberry Pi Imager
- Wi-Fi, user and SSH are set with Raspberry Pi Imager's settings as for any Raspberry Pi OS image.
- `deploy/install.sh` keeps working on an existing Raspberry Pi OS and does the same.

## 6. Onboarding wizard (spec 03 §12)
| Step | Status | What |
|---|---|---|
| 1. Language | ✓ | – |
| 2. Fresh or restore | ✓ M7 | "Start fresh" or "Restore a backup": file + passphrase (§2), open to whoever holds the wizard (`POST /api/onboarding/restore`); the kicker restarts with the backup, which ends the wizard |
| 3. Admin account | ✓ | – |
| 4. Emergency kit | ✓ | "Finish setup" ends onboarding and leads on to `/setup/more` |
| 5. Table | ✓ M7 | side names + colors (the settings form) |
| 6. Game defaults | ✓ M7 | preset and game defaults, inactivity (the settings form) |
| 7. Modules | ✓ M7 | discovered modules with the live console: identify, adopt, roles (the modules page) |
| 8. Backups + updates | backups ✓ M7; updates with task 5 | the backup cycle and locations (the settings component); update check on/off, install automatically on/off |
| 9. Done | ✓ M7 | the kicker's address, a link to the CA help page (`/trust`), then the scoreboard. The QR code for the app comes with M4 |
- Steps 5–8 have "Back" and "Skip"; they reuse the settings components (`TableSettingsForm`, `ModulesPanel`, `BackupConfigForm`), so the wizard and the settings never differ. Onboarding is already finished there (admin only); the step survives a reload.

## 7. Permissions
All of this is Admin-only (spec 03 §4: backups and updates are system permissions). Restore during onboarding is open to whoever holds the wizard.

## 8. Milestone tasks (proposed order)
1. ✓ Backups: format, `VACUUM INTO` snapshot, encrypt/decrypt, restore with re-exec and swap, retention, scheduler, before migrations; `kicker backup` / `kicker restore` CLI.
2. ✓ Backups UI: Settings → Backups (cycle, locations, the list with restore/download/delete, upload), change passphrase; admin badge.
3. ✓ Onboarding steps 2, 5–9 (the update toggles of step 8 come with task 5).
4. ✓ Releases: `release.json` + minisign in CI, `kicker release` (keygen, manifest, sign, verify) for local test releases.
5. Server updates: check, install, health check, rollback; Settings → Updates; `kicker update`.
6. Firmware updates over USB: bootloader drive detection and UF2 writing, the flow, modules page; tested on the bench rig with a Pico W / RP2040-Zero.
7. SD card image + udev rules + install script changes; tested on a Pi 4.
8. End-to-end: backup → restore on a fresh server; an update from a locally signed test release, including a forced rollback (Playwright + a Go test).

## 9. Decisions (owner, 2026-11-25)
- **A. The binary lives in the data folder** (`<data>/bin/current`), so the service updates itself without root. A compromised service already has all the data.
- **B. Releases come from `Humbarrt/kicker`, a public repo** (owner, 2026-11-26: published before 1.0 – no token needed; replaces "private until 1.0 with a read-only token").
- **C. Writing the `.uf2`:** straight to the bootloader's block device (a udev rule gives `kicker` access; no mounting, no root) – confirmed on the bench rig; if that fails, mounting the drive (udisks2 + polkit) and copying the file.
- **D. The image is the official Raspberry Pi OS Lite image, modified in CI** (loop-mounted, files copied, unit enabled, compressed).
- **E. Firmware updates are always by hand**, announced with a badge. Server updates are installed automatically only if that setting is on; otherwise the same alert.
- **F. A failed update rolls back automatically and restores the pre-update backup.**
- **Several backup locations** at once (§2); the backup page is laid out like Home Assistant's (cycle, locations, list, upload; 2026-11-26).
