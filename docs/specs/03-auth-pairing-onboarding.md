# Spec 03: Auth, pairing + onboarding

Status: **Approved v4** (2026-11-20: groups with editable permissions replace the fixed roles). Builds on D3, D4, D14, D18 in `00-architecture-decisions.md` and on the corner claim flow in `02-game-engine.md` §5.

## 1. Scope
- Accounts, groups, permissions, member approval
- Sessions and login security
- Password reset and the admin recovery key
- Encrypted backups (key handling)
- HTTPS certificates and the server's identity
- Pairing the app with one or more kickers
- Corner NFC tags
- The first-boot onboarding wizard
- General API security

## 2. Accounts and players
- An **account** is a login. A **player** is someone who appears in games. They're separate (D3):
  - A player can exist without an account (created by a member, or from a coin).
  - An approved account is linked to **exactly one** player, and a player has **at most one** account.
- Account fields: `username` (3–32 chars, `a-z 0-9 _ . -`, unique, case-insensitive), password hash, `group_id` (§4), `status` (`pending` / `approved` / `disabled`), `player_id`, `created_at`, `last_login_at`.
- **Passwords:** at least 10 characters and not in a list of the 10,000 most common passwords of that length. No other composition rules (NIST 800-63B style).
- **Hashing:** argon2id in the standard PHC string format, so the parameters are stored with each hash. During onboarding the server calibrates the parameters so that one hash takes about 250 ms on that Pi. Existing hashes are upgraded on the next successful login if the parameters change.

## 3. Sign-up and approval
- **Registration** (setting `registration`):
  - `approval` (default): new accounts are `pending` until an admin approves them.
  - `open` into a chosen group: new accounts are approved right away and join that group. Meant for a trusted home network. The group can't be Admin.
  - `closed`: only admins create accounts.
- **Sign-up form:** username, password, and "who are you?":
  - pick an existing player that has no account yet, or
  - create a new player (name; color picked automatically, D2)
- The sign-up only records the choice (`requested_player_id` / `requested_player_name`); nothing is linked or created before an admin approves. A new player is created on approval, with the name the admin confirmed.
- A **pending** account can log in, but only sees "Waiting for approval". It has exactly the permissions of the **Anybody** group (§4).
- Sign-ups are throttled per IP address like logins (5 freely, then a growing delay).
- **Admins** get a badge and a live notification (SSE) for pending accounts. They can:
  - approve: choose the group (default: the setting `default_group`, initially Member) and confirm or change the linked player. **Linking to an existing player always needs this confirmation**, so nobody can take over someone else's history. With `open` registration, linking to an existing player is not offered; the new account gets a new player.
  - reject: the account is deleted.
- Admins can also create accounts directly (already approved, in any group) with a temporary password that must be changed at first login.
- Admins can move accounts between groups and disable accounts. Disabling revokes all of the account's sessions.

## 4. Groups and permissions
Every account is in **exactly one group**. Admins create, rename and delete groups, edit their permissions, and move people between them.

### Built-in groups
| Group | Who is in it | What can be changed |
|---|---|---|
| **Admin** | chosen accounts | the name only. All permissions are always granted, including the system permissions. **The last approved admin can't be moved, disabled or deleted.** |
| **Member** | default group for new accounts | name and permissions; can't be deleted |
| **Anybody** | everyone not logged in, plus pending accounts | name and permissions; can't be deleted; no account can be put into it |

- **Everyone also has Anybody's permissions.** A group's effective permissions are its own combined with Anybody's (the more generous value wins). So a box ticked for Anybody never has to be ticked again for the other groups.
- **Own groups** (e.g. "Guest", "Office", "Tournament crew") are created as a copy of an existing group.
- **Deleting a group** moves its accounts to a group the admin chooses.
- Permissions added in a later release get their default for the built-in groups and are off for own groups.

### Permissions
Each permission has a kind: **on/off**, **scope** (off / own / all), or **days** (1–365 or unlimited).

| Area | Permission | Kind | Login required | Default Member | Default Anybody |
|---|---|---|---|---|---|
| Viewing | `view.live` – live table, scoreboard | on/off | | on | on |
| | `view.stats` – stats, games, players | on/off | | on | on |
| | `view.tournaments` | on/off | | on | on |
| Live table | `table.join` – join with the phone (corner claim) | on/off | ✓ | on | – |
| | `table.score` – +1 / −1 / undo | on/off | | on | off |
| | `table.control` – start, pause, resume, "Start new game" | on/off | | on | off |
| | `table.players` – add / remove players in the current game | on/off | | on | off |
| | `table.rules` – change the win conditions of the current / next game (a copy; the preset stays unchanged) | on/off | | on | off |
| | `table.preset` – choose the preset for the next game | on/off | | on | off |
| | `table.balls` – reset the ball count | on/off | | on | off |
| Players & coins | `players.create` | on/off | ✓ | on | – |
| | `profile.own` – edit own name, picture, color | on/off | ✓ | on | – |
| | `profile.any` – edit other players' profiles | on/off | ✓ | off | – |
| | `coins.manage` – assign, reassign, deactivate coins | on/off | ✓ | on | – |
| Finished games | `games.edit` – which games can be edited | scope | ✓ | own | – |
| | `games.edit_days` – how far back | days | ✓ | 7 | – |
| | `games.revert` – revert edits | on/off | ✓ | off | – |
| | `games.void` – void games | on/off | ✓ | off | – |
| Tournaments | `tournaments.create` | on/off | ✓ | on | – |
| | `tournaments.run` – start matches, enter results | scope | ✓ | own | – |
| | `tournaments.edit` – settings, participants | scope | ✓ | own | – |
| | `tournaments.manage` – abandon, reinstate, rebuild brackets, delete | on/off | ✓ | off | – |
| Rule presets | `presets.manage` – create, edit, delete | on/off | ✓ | off | – |
| Log | `log.view` – read the audit log (Settings → Audit log) | on/off | ✓ | on | – |
| | `log.ip` – … with the IP addresses | on/off | ✓ | off | – |

- **"Own"** means: games the account's player took part in; tournaments the account created.
- **Login required:** these need to know who acts, so they can't be granted to Anybody. Anonymous table actions (if Anybody gets them) are logged without an author.
- **System permissions – Admin group only, never grantable:** settings, modules and the module console, accounts and groups, backups, updates. Whoever manages accounts or groups can make themselves admin, and modules or settings can break the table, so granting them would only create admins by another name.
- Every change to groups, permissions, group membership and account status is written to the audit log.
- Changing the win conditions of a running game is logged as a game event (`rules_changed`, spec 02 §7.3).
- **Implementation:** gin middleware `Require(permission)` and `RequireAdmin` (system). Every check also happens in the service layer, not only in the router. The effective permissions of each group are cached in memory and reloaded whenever an admin changes a group. `GET /api/auth/me` returns the account, its group and its effective permissions, so the UI can hide what isn't allowed.

## 5. Sessions
- **Token:** 32 random bytes, base64url. The DB stores only `sha256(token)`.
- **Session row:** `id`, `account_id`, `token_hash`, `kind` (`web` / `app`), `device_name`, `user_agent`, `created_at`, `last_seen_at`, `expires_at`.
- **Lifetime** (renewed on use):
  - web: expires after 30 days without use
  - app: expires after 365 days without use
- `last_seen_at` is written at most once every 5 minutes, to avoid constant DB writes.
- **Web:** cookie `__Host-kicker_session`, with `Secure; HttpOnly; SameSite=Strict; Path=/`. The `__Host-` prefix makes the browser refuse the cookie unless it's set exactly like that. Over plain HTTP (M1–M2) the cookie is `kicker_session` without `Secure`.
- **App:** `Authorization: Bearer <token>`. The app keeps tokens in the operating system's secure storage (Keychain / Android Keystore).
- **"My devices" page:** lists all sessions with device name and last use; any of them can be revoked.
- **Revoked automatically:**
  - on password change: all *other* sessions
  - on password reset: all sessions
  - on disable: all sessions
- Session lookups are cached in memory for at most a minute and the cache is cleared on revoke and on group or status changes (single process, so this is simple).

## 6. Login security
- **Throttling** per IP address and per username:
  - after 5 failures, each further attempt is delayed exponentially (1 s, 2 s, 4 s, … up to 15 min)
  - a successful login resets the counter
- Unknown usernames get the same error and take the same time as wrong passwords: a dummy hash is checked so the timing matches.
- Login failures in bursts, approvals, group and permission changes, password resets, recovery key use and session revokes are written to `audit_log`, visible with `log.view` (IP addresses only with `log.ip`).

## 7. Password reset
There's no email (offline system), so resets go through people or the device.
- **Members:**
  1. An admin clicks "reset password" for the member.
  2. The server shows a one-time **reset code** (8 characters, valid for 24 h).
  3. The admin passes it on in person.
  4. The member enters the code and a new password.
- **Admins: the recovery key** (D18):
  - 12 words from the BIP39 English word list (132 bits).
  - Stored only as an argon2id hash.
  - "Forgot password" on the login page → username + recovery key + new password.
  - After it has been used, **a new recovery key is generated and must be saved**, because the old one may have been seen. All sessions of that admin are revoked.
  - An admin can regenerate the key at any time in the settings, which invalidates the old one.
  - Attempts are throttled like logins. Unknown usernames, accounts that aren't admins and wrong keys get the same answer.
  - The words may be typed in any case, with or without numbers ("1. abandon …").
- **Reset codes** look like `ABCD-EFGH` (letters and digits without 0/O, 1/I/L); case, spaces and dashes don't matter. Using one ends all sessions of the account and clears "must change password". One code per account; a new code replaces the old one.
- **Physical access (last resort):** on the Pi, `kicker admin create <username>` creates an admin (and completes the setup wizard if needed), and `kicker admin set-password <username>` sets a new password and ends all sessions of that account, and `kicker admin reset-code <username>` prints a reset code. Whoever has a shell on the Pi controls it anyway.

## 8. Encrypted backups
Backups contain secrets (CA key, password hashes, recovery key hash) and may end up on a USB stick or a network share. **Every backup is encrypted, always.**
- **Format:** the `.tar.gz` from D14 is encrypted with [age](https://age-encryption.org) using a passphrase (`filippo.io/age`, pure Go). The file is named `kicker-backup-<name>-<date>.tar.gz.age`.
- **Backup passphrase:**
  - Generated during onboarding: 8 words from the BIP39 list. The admin may replace it with their own (at least 16 characters).
  - The server keeps it in the data folder (mode 0600), so scheduled backups run without anyone present. The goal is to protect copies that **leave** the Pi; whoever has the Pi itself has the data anyway.
  - **Separate from the recovery key** on purpose: the recovery key changes after every use, and old backups must stay restorable.
- **Changing the passphrase** only affects new backups. The UI warns that older backups still need the old passphrase, and offers to create a fresh backup right away.
- **Restoring** (settings or onboarding): upload the backup file + enter the passphrase. A wrong passphrase or a damaged file is reported before anything is overwritten.
- Downloaded backups from the UI are encrypted the same way.

### Emergency kit
The recovery key and the backup passphrase are handed out together as one **emergency kit**:
- shown on screen and offered as a download `kicker-emergency-kit-<name>.txt`
- contains: kicker name, `server_id`, CA fingerprint, recovery key, backup passphrase, and short instructions for "forgot admin password" and "restore on a new Pi"
- available again in Settings → Security (admin, after re-entering the password). Only a hash of the recovery key is stored, so "again" means: a **new** kit with a new recovery key; the old key stops working. The backup passphrase stays the same until it is changed (M7).
- Until a kit exists (e.g. after `kicker admin create`), admins see a badge on Settings → Security.

## 9. HTTPS and server identity
### Identity
- On first boot the server creates:
  - `server_id`: random UUID
  - a **local CA**: ECDSA P-256, 20 years, named "Kicker Local CA (<name>)"
  - a **server certificate** signed by that CA
- These are included in the (encrypted) backups, so **a restored or moved kicker keeps its identity** and paired apps keep working.
- **`GET /api/info`** (no login needed) returns `server_id`, `name`, version, CA fingerprint and `onboarding_done`.

### Server certificate
- Valid for 397 days, renewed automatically 30 days before it expires. Apple devices refuse certificates valid for longer than about two years.
- **Names it covers:**
  - `kicker.local`
  - the actual mDNS host name (it may differ, e.g. `kicker-2.local`)
  - `localhost`
  - all current IP addresses
- If the IP addresses or the host name change, the certificate is reissued automatically (checked at start and every 10 minutes). Apps pin the **CA**, not the server certificate, so reissuing breaks nothing.
- **Implementation (M3):** package `tlsca`. The names are `kicker.local`, `localhost`, the machine's host name (plain and `.local`), extra names from `--tls-names`, and all non-link-local addresses plus loopback. The host name comes from the system (`os.Hostname`), which is what avahi announces; reading avahi's conflict name (`kicker-2.local`) over D-Bus follows with the mDNS service (M4). A CA with only one of its two files is an error, never silently replaced, because a new CA is a new identity.
- `kicker serve --addr :80 --tls-addr :443` turns HTTPS on (the systemd unit does). Without `--tls-addr` the server speaks plain HTTP only – for development (`make dev`).

### Plain HTTP (port 80)
- Redirects to HTTPS, temporarily (302, or 307 for other methods than GET) – a permanent redirect would be cached like HSTS.
- Also serves `/ca.crt` and the "trust this kicker" help page `/trust` with the fingerprint and instructions per operating system (both also over HTTPS).
- **No HSTS on purpose:** after a reinstall *without* a backup, the kicker has a new CA. HSTS would then lock browsers out instead of showing the normal warning.

### Browsers
- On the first visit, the browser shows a certificate warning. Users can either accept it, or install the kicker's CA (optional, via the help page) to get rid of the warning for good.
- The CA fingerprint is shown in Settings → Security, during onboarding and in the emergency kit, so it can be compared.

### mDNS
- Host name `kicker.local` (via avahi). If another device on the network already uses it, avahi picks `kicker-2.local`; the server reads the actual name and adds it to its certificate.
- Service `_kicker._tcp` with TXT records `id=<server_id>`, `name`, `ver`.

## 10. Pairing the app
### Adding a kicker
The app offers three ways:
1. **Found nearby:** a list of kickers found via mDNS (`_kicker._tcp`).
2. **Enter URL:** default `https://kicker.local`.
3. **Scan QR code:** shown in the web UI under Settings → "Connect app". The QR contains the URL, `server_id` and CA fingerprint. This is the safest way: nothing has to be trusted blindly.

For options 1 and 2 the app trusts the kicker on first use:
- It shows "Kicker 'Office' · fingerprint `AB12 CD34 …`". Once the user confirms, the app pins that CA for this `server_id`.
- If the CA ever changes, the app shows a clear error ("This kicker's identity has changed") and only re-pairs after explicit confirmation.

### Several kickers (work + home)
- The app stores a list of `{server_id, name, url, ca_pin, token}` and is logged in separately on each kicker.
- If a URL leads to a different `server_id` than expected: "This is a different kicker ('Home'). Add it?"
- If the kicker can't be reached: "Server not found. Edit URL?" (D4).

### Implementation note (Tauri)
- The webview's own `fetch` can't pin certificates.
- So in the app, **all API and SSE traffic goes through the Rust side**, using rustls with a custom certificate check against the pinned CA. The Svelte code calls a small API client that uses `fetch` in the browser and Tauri commands in the app.

## 11. Corner NFC tags
- **Hardware:** NTAG213/215 stickers, one per corner.
- **Content:** one NDEF URI record, `https://112233444.xyz/c?s=<server_id>&c=home_1` (decided 2026-11-25, spec 08). The domain is the project's link domain: static files only, the kicker is never on it.
  - The `server_id` lets the app pick the right account (work vs home) automatically.
  - No secret on the tag: faking a presence isn't a concern (decided).
- **Programming:** in the app, admin menu "Program corner tags" → choose a corner → hold the phone to the tag. The app writes the tag and can lock it as read-only.
- **Tapping a tag:**
  - **App installed (iOS and Android):** the URL is a universal link / verified App Link, so the phone opens the app – on iOS after tapping the notification – and the app sends the claim to the kicker over the local network.
  - **No app:** the phone opens the link in the browser; the page there forwards to `https://kicker.local/claim?s=…&c=…`, the kicker's own web UI, which claims the corner with the browser's login.
  - Custom schemes like `kicker://` don't work: iOS background tag reading ignores them (spec 08).
  - The app also has "Join" → hold the phone to the tag, for phones without background tag reading.
- **Claim:** `POST /api/table/claims {corner}` with a token of an account that has `table.join` (spec 02 §5).
- **Corners work without a presence sensor:** if no `corner_presence` module is adopted for that corner, a claim simply counts for one game (spec 02 §5, rule 5).

## 12. Onboarding wizard
**Principle: onboarding never depends on hardware.** It works with no modules connected, and nothing in it can get stuck because of a module.

While `onboarding_done` is false:
- the web UI only shows the wizard
- only `/api/info` and `/api/onboarding/*` answer; everything else returns 503
- the first browser to start the wizard gets an onboarding cookie, and other browsers see "Setup in progress". A claim that has been idle for 15 minutes can be taken over.
- As in Home Assistant, whoever starts the wizard first becomes admin. This is accepted: the server only runs on the local network.

| Step | Content |
|---|---|
| 1. Language | for the UI and the displays |
| 2. Fresh or restore | upload an encrypted backup + passphrase (§8). Restoring brings back everything, including identity and accounts, and ends the wizard |
| 3. Admin account | username, password, optionally "create a player for me". The argon2id parameters are calibrated here. The account joins the Admin group |
| 4. Emergency kit | recovery key + backup passphrase (§8). Continue only after ticking "I have saved it". This step belongs to the admin made in step 3 (logged in right after it), not to the browser's claim: during setup, login is allowed, and a lost tab logs in again instead of handing the kit to whoever claims the wizard next |
| 5. Table | side display names + colors (D17) |
| 6. Game defaults | current preset, `pause_after_minutes`, `sleep_after_minutes`, `on_wake`, `kickoff_requires`, first-ball rule |
| 7. Modules | discovered modules, with their live module console: identify (blink), adopt, assign roles. **Skippable**, and can be done any time later in the settings |
| 8. Backups + updates | schedule and destination, automatic update check on/off |
| 9. Done | QR code for pairing the app (§10), link to the CA help page, then the dashboard |

- Steps 5–8 can all be skipped and changed later. Only steps 1–4 are required, so a problem with hardware or settings never blocks finishing setup.
- The module console (D8) is available in step 7 and, after onboarding, to admins in the settings, so a broken module can always be debugged.
- Implemented so far: steps 1, 3 and 4 (M3); the others come with their milestones.

## 13. General API security
- **CSRF:** for requests authenticated by cookie, every change (non-GET) also requires an `Origin` header matching the kicker itself. A foreign `Origin` is always refused. This comes on top of `SameSite=Strict`. Bearer-token requests (the app) aren't affected.
- **Headers:**
  - a strict Content Security Policy (only the kicker itself as source)
  - `X-Frame-Options: DENY`
  - `Referrer-Policy: no-referrer`
  - `X-Content-Type-Options: nosniff`
- Uploads (profile pictures, backups) have size limits and their file types are checked by content, not by name.
- The SSE stream uses the same session (cookie or bearer) and requires `view.live`.

## 14. Tables
- `groups` (id, name, builtin `admin`/`member`/`anybody` NULL, created_at)
- `group_permissions` (group_id, permission, value). Values: on/off = 0/1; scope = 0 off, 1 own, 2 all; days = 0 off, 1–365, −1 unlimited. Missing row = off.
- `accounts` (id, username, password_hash, group_id, status, player_id NULL, must_change_password, created_at, last_login_at, requested_player_id NULL, requested_player_name NULL)
- `sessions` (id, account_id, token_hash, kind, device_name, user_agent, created_at, last_seen_at, expires_at)
- `reset_codes` (account_id, code_hash, expires_at, created_by)
- `recovery_key` (hash, created_at) – one row
- setting `registration` (mode, open_group_id, default_group_id)
- `audit_log` (id, at, account_id NULL, ip, action, target, details)
- `login_throttle` (key, failures, next_allowed_at). Lives in memory.
- Database triggers guard the rules that must never break: built-in groups can't be deleted, no account can be in Anybody.
- Files in the data folder (mode 0600): `ca.key`, `ca.crt`, `server.key`, `server.crt`, `backup.passphrase`

## 15. Decided in review
- (2026-11-20) Groups with editable permissions instead of fixed roles: one group per account; Admin (fixed, all permissions), Member and Anybody built in; Anybody's permissions apply to everyone; system permissions stay admin-only; registration can be `open` into a chosen group.

## 16. Open questions
- None.
