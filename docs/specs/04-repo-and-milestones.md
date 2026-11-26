# Spec 04: Repository layout, tooling + milestones

Status: **Approved v6** (2026-11-23: software-first build order, §7). Turns specs 00–03 into a codebase and a build order.
Progress: **M1 ✓** (2026-11-23). M3: tasks 1–12 ✓; open: the load test on the Pi. M6: tasks 1–7 ✓; open: the load test on the Pi (with M3's). M7 in progress (spec 10): tasks 1–3 ✓.

## 1. One monorepo
Everything lives in one **private** git repository on the owner's GitHub (`Humbarrt/kicker`), so the protocol, the server, the Arduino library and the UI always change together, and one version tag produces one release (D15). The old attempts live in a separate `kicker_old` folder and are not part of the repo.

```
kicker/
├── server/                    Go module github.com/Humbarrt/kicker/server
│   ├── cmd/kicker/            one binary, subcommands: serve, monitor, admin, loadtest
│   ├── internal/
│   │   ├── engine/            pure game engine: Step(state, input, now) – no I/O (spec 02)
│   │   ├── table/             the actor around the engine: persists events, runs effects, timers
│   │   ├── modules/           module host: discovery, adoption, roles, console buffer
│   │   │   ├── session/       one protocol conversation with one module (used by host + monitor)
│   │   │   ├── transport/     serial (go.bug.st/serial)
│   │   │   └── virtual/       virtual modules for the simulator
│   │   ├── protocol/          wire messages, line framing, strict decoding, capability catalog,
│   │   │                      dedup window, JSON Schema generation (spec 01) – stdlib only
│   │   ├── store/             SQLite: connection, single writer, hand-written queries
│   │   │   └── migrations/    embedded SQL migrations (NNNN_name.sql, forward only)
│   │   ├── ratings/           Elo as a pure function over the game log (D9)
│   │   ├── auth/              accounts, sessions, throttling, recovery key (spec 03)
│   │   ├── api/               gin routes, middleware, DTOs, SSE hub
│   │   ├── webui/             embedded web build + SPA fallback
│   │   ├── bus/               in-process event bus
│   │   ├── sim/               simulator: scenario runner
│   │   └── …                  later: tournament/, backup/, update/, tlsca/, mdns/, onboarding/, i18n/
├── web/                       SvelteKit 3 (adapter-static), pnpm, Tailwind 4, shadcn-svelte
│   └── src/lib/api/           API client: fetch in the browser, Tauri commands in the app
├── app/                       Tauri v2 shell around the web build
│   └── src-tauri/             Rust: pinned-TLS API/SSE client, NFC, mDNS, secure storage
├── firmware/
│   ├── lib/KickerModule/      Arduino library: protocol core (plain C++, PC-tested), capability
│   │                          helpers, hardware components (GoalGate, …)
│   └── modules/               PlatformIO projects: goal-sensor, coin-acceptor, corner-presence,
│                              ball-dispenser, display – each a list of components with pins
├── catalog/                   generated JSON Schemas (protocol + one per capability)
├── hardware/                  module CAD/STL, wiring diagrams, BOMs, bench rig (§9)
├── docs/                      specs (copied from the project), build guide, module dev guide
├── deploy/                    systemd unit, install script
├── tools/                     helper scripts
├── Makefile
└── .github/workflows/
```

**Why this layout:**
- `engine/` has **no imports** from `store/`, `api/` or `modules/`. It can be tested on its own, and the simulator, the load test and the real table all run the same code.
- Everything outside `engine/` is plumbing that feeds it inputs and carries out its effects.
- `protocol/` is the single source of truth for module messages. JSON Schemas for third-party builders are generated from it (`make generate`, also run before dev/build/test), never written by hand. A test fails if `catalog/` is out of date.

### Reuse from v1/v2
- v1: the `ScoreTarget` win formula (now `engine.Rules`), the inactivity/duration ideas, the presets idea, the `dev` page concept (becomes the simulator panel).
- v2: the shadcn-svelte + layerchart setup of `kicker_frontend`, the Tauri app skeleton.
- Not reused: protobuf/COBS framing, the Rust firmware, the JWT auth, goose + sqlc (see §2).

## 2. Tooling
| Area | Choice |
|---|---|
| Server | Go 1.25, gin, go.bug.st/serial, `ncruces/go-sqlite3` (SQLite compiled to WebAssembly, no cgo), `tygo` (TypeScript types from the API DTOs), golangci-lint |
| Database | own embedded migration runner (~80 lines, forward only, refuses a database newer than the binary); queries hand-written in `store/`. sqlc can be added later if the query count grows |
| JSON Schemas | own reflection-based generator in `protocol/schema.go` (no dependency). Field rules shared with the decoder: no `omitempty` = required, `enum`/`min`/`max`/`doc` tags |
| Web | pnpm 10, **SvelteKit 3** / Svelte 5 (config lives in `vite.config.ts`), Tailwind 4, shadcn-svelte (bits-ui), layerchart; icons only from `@lucide/svelte` (ISC, deep imports `@lucide/svelte/icons/<name>`), no hand-drawn SVG icons |
| App | Tauri v2, Rust stable |
| Firmware | PlatformIO, `arduino-pico` core (RP2040/RP2350) via the maxgerhardt platform, ArduinoJson 7. Test boards: RP2040-Zero (reference), Pico W (`-e pico-w`) |
| Build | `make` (as in v1/v2) |
| Target | **Raspberry Pi 4, 4 GB**, Raspberry Pi OS Lite 64-bit (`linux/arm64`). Because the SQLite driver needs no cgo, cross-compiling is just `GOOS=linux GOARCH=arm64 go build` |

**SQLite setup:**
- WAL mode, `foreign_keys=on`, `busy_timeout=5000`, all tables `STRICT`, times as Unix milliseconds.
- **One writer connection** (transactions start `IMMEDIATE`) plus a **read-only pool** of 4. Writes are serialized in the process; reads never block on them.
- `synchronous=NORMAL`: a power cut can lose the last few committed transactions (at most the last goal or two), but never corrupts the database. Chosen for SD card wear and goal latency; can be switched to `FULL` if that ever matters.
- The driver is pinned to v0.30.x because newer releases need Go 1.26.

**Shared types:**
- API DTOs (Go) → `tygo` → `web/src/lib/api/types.ts`
- Wire messages (Go) → JSON Schema → `catalog/`
- The `KickerModule` capability helpers (C++) are written by hand. There are only 5 capabilities; an end-to-end check feeds the library's output through the Go decoder.

## 3. Development without hardware
- **`kicker serve --sim`** starts the server with **virtual modules** for every catalog capability: two goal sensors, two coin acceptors, four corners, a dispenser and two displays.
  - A **simulator panel** in the web UI (dev builds only) has buttons for "goal home", "insert coin X", "phone on corner 2", "eject", "dispenser empty", …
  - **Virtual displays** render the exact `state` and `anim` commands a real display would get, in the browser. Display layouts and texts can be designed before any screen is wired up.
  - The virtual modules use the same transport interface as the serial ones, so everything above the transport is the real code.
- **`make dev`** runs `kicker serve --sim` plus the Vite dev server, with the API proxied, and hot reload on both sides.
- **`kicker monitor [port]`** is for firmware work: it plays the server side of the protocol for one module, shows every line with its validation result, lets you send commands by hand, and reconnects when the board resets (D8).
- Simulated and real modules can be mixed: e.g. a real goal sensor on the bench plus virtual coin acceptors.

## 4. Tests
| Layer | How |
|---|---|
| Engine | unit tests of `Step`, a randomized "replay = live state" test, plus **scenario files** (`engine/testdata/scenarios/*.txt`, one command per line: `start official`, `wait 3s`, `sensor home_goal`, `undo`, `restart`, `expect score 6:4` …; plain text instead of YAML, so no dependency). Every rule in spec 02 gets at least one scenario |
| Protocol | table tests for every message type and error code, Go fuzzing of the line reader and the decoder, catalog payload tests, schema tests, "catalog up to date" check |
| Session | a fake module scripted in Go over an in-memory pipe: handshake, duplicates, invalid events, retries, timeouts, offline/online, reboot detection; run with `-race` |
| Store | real SQLite in a temp folder: migrations from scratch and reopen, constraints (CHECK, unique roles, cascades), concurrent writers and readers |
| API | `httptest` with the real router and a temp DB; the permissions of spec 03 §4 tested per route, including Anybody grants and the Admin-only system routes |
| Firmware library | PlatformIO `native` environment: protocol core, `BeamDetector`, component hooks – plain C++ tests on the PC |
| End to end | Playwright against `kicker serve --sim` (from milestone 3 on) |
| Load | `kicker loadtest`: generates 100k games **through the real engine** into a DB, then measures the budgets below |

**Load test budgets**, measured on the **Pi 4 (4 GB)** before each release (`make loadtest`); CI runs a 300-game version as a Go test, and the browser tests (`make e2e`):
- stats pages and player profiles, as served: p95 < 150 ms with 100k games
- a stats view computed in the background: < 500 ms each (it never holds up a request, decision B)
- rating replay after editing the oldest game: < 3 s (within the tolerance of decision A)
- goal → SSE message at the browser: < 50 ms; goal → dispenser `release` command: < 10 ms
- tournaments (M6): a 64-team double knockout and a 40-player Monster DYP played to the end on the table (every pairing by "Play", goals by the sensor): the tournament page p95 < 150 ms after every pairing, and a pairing's last goal → the tournament has its result p95 < 150 ms
- SSE: 50 connected clients without slowing down the table

**Task 11 decisions** (2026-11-24, approved by the owner):
- **A. Replay tolerance.** Elo never fully converges after an old edit: the difference spreads over all players, and rounding to 0.01 keeps ±0.02 of noise alive for good. Voiding the oldest of 100k games changed 198 000 of 329 000 rating rows (11.5 s, holding the single writer the whole time). The replay stops once every rating is within **0.05** of the stored one, past the edited game: about 30 000 rows, 1.1–1.5 s. Later rows can differ from an exact recompute by < 0.1 (ratings show as whole numbers); a full replay (new stats version, `ReplayAll`) is exact.
- **B. Stats are cached and refreshed in the background**, like a scheduled task. Profiles, both leaderboards and the overview are computed at start-up and kept in memory (`stats.Cache`); requests always get the cached version at once. After every replay (a finished or edited game) the cache recomputes them in the background – overview and leaderboards first, then the profiles – and announces each part on the `stats` SSE topic, so the pages reload when the new numbers are there. It also refreshes every hour (the 30-day window moves). Only a view nobody has asked for since start-up and that the start-up refresh hasn't reached yet is computed for its request.
- **C. Ratings are rounded to 0.01 after every game** inside the replay too (stats version 2: one full replay on update), so many small replays give the same numbers as one full one.

Measured on x86 (100k games, 40 players, 329 000 rating rows; the machine's speed varied between runs by up to 3×), before the Pi run:

| | p50 | p95 | budget |
|---|---|---|---|
| stats endpoints, served (overview, leaderboards, profiles) | ≤ 0.3 ms | ≤ 0.6 ms | 150 ms ✓ |
| game list (two players against each other / filtered), game | 82 / 41 / 2 ms | 118 / 92 / 3 ms | 150 ms ✓ |
| overview / leaderboard / busiest profile (13 500 games), computed | 69 / 65 / 237 ms | 87 / 93 / 274 ms | 500 ms ✓ |
| start-up: every view computed | 6.4 s | | – |
| void the oldest game / take it back: ratings replayed | 1.5 s / 1.4 s | | 3 s ✓ |
| … every stats view refreshed (background) | 6.5 s / 6.2 s | | – |
| full replay (100k games, nothing changed) | 2.7–7.3 s | | – |
| goal → saved, effects out (50 SSE clients) | 1.3 ms | 2.4 ms | 10 ms ✓ |
| goal → SSE message at 50 clients | 5.5 ms | 10 ms | 50 ms ✓ |
| 64-team double knockout (126 pairings): tournament page / last goal → result | 6.4 / 8.7 ms | 13 / 16 ms | 150 ms ✓ |
| 40-player Monster DYP (50 pairings): tournament page / last goal → result | 5.4 / 11 ms | 12 / 22 ms | 150 ms ✓ |

The first tournament run (M6 task 7) found the page at 290 ms p95: the pairing results query scanned all 100k games, because SQLite doesn't use the partial index `games_pairing` unless the query says `pairing_id IS NOT NULL`. Fixed (1 ms instead of 90 ms).

On the Pi (roughly 3× slower) the background refresh will take about 20 s with 100k games: the old numbers show until then. Measure before M3 closes.

## 5. CI and releases
- **On every push** (GitHub Actions, private repo):
  - Go: vet, tests (incl. the catalog-up-to-date test), arm64 build
  - web: `svelte-check`, build
  - firmware: native library tests, build every module for its reference board
- **On a tag `vX.Y.Z`:**
  1. build `kicker` for `linux/arm64` (and `amd64` for testing), with the web build embedded
  2. build all firmware `.uf2` files and the `catalog/` schemas
  3. sign everything with the minisign key from the repo secrets, and set the update repo + public key via `-ldflags` (D15)
  4. publish a GitHub release
  - Forks don't have the signing secret, so their builds have updates disabled.
  - While the repo is private, the Pi needs a read token to fetch releases. The update check becomes token-free once the repo is public.
- **Versioning:** one semver for the whole release. The protocol version (spec 01) and capability versions are separate and only change on breaking changes.

## 6. Deployment on the Pi
- **Hardware:** Raspberry Pi 4 (4 GB). Preferably boots from a USB 3 SSD; a high-endurance SD card is fine to start with.
- `deploy/install.sh` (run once on Raspberry Pi OS Lite 64-bit):
  - creates the `kicker` system user, member of `dialout` (serial ports)
  - installs the binary to `/opt/kicker/kicker` (the previous version is kept for rollback)
  - data folder `/var/lib/kicker` (database `kicker.db`)
  - systemd unit with `AmbientCapabilities=CAP_NET_BIND_SERVICE`, so it can use ports 80/443 without root
  - avahi host name `kicker`
- After that: `make deploy PI=pi@kicker.local`.
- **HTTPS** with the kicker's own CA (spec 03 §9): the unit runs `kicker serve --addr :80 --tls-addr :443`; port 80 only redirects and serves `/ca.crt` and `/trust`.
- Later: a ready-made SD card image (pi-gen) so non-technical builders can just flash and boot.

## 7. Milestones
Each milestone ends with something you can play with. Tasks inside a milestone are roughly in order.

**Build order (software first, 2026-11-23):** M1 ✓ → **M3** → M6 → M7 → M8 → M2 → M4 → M5.
The milestone numbers are names, not positions: they stay the same so references in the specs and the code (`TODO(M3)`, …) keep their meaning. The milestones that need new hardware or a native app – the side displays (M2), the phone app with corner tags (M4) and the ball dispenser (M5) – come last. Until then the simulator stands in for every module (§3), and all hardware is designed and built in the separate hardware chat (specs 05–07).

### M1: "First goal" – goal sensor → live score on the web
**Done when:**
1. A goal sensor module (one RP2040-Zero, two IR break-beam sensors) is plugged into the Pi and shows up in the modules page within 3 s. It can be adopted and its two instances assigned `home_goal` / `away_goal`.
2. After logging in and pressing "Start now", breaking a beam updates the web scoreboard in under 100 ms.
3. The game ends at the preset's target score, and the game and all its events are stored.
4. Restarting the server mid-game: the game continues where it was.
5. Unplugging and replugging the module mid-game loses no goals.
6. The same flow works in `kicker serve --sim` without any hardware.

**Tasks:**
1. ✓ Repo skeleton, Makefile, CI for Go + web + firmware.
2. ✓ `protocol/`: message types, line reader, strict decoder, capability catalog, dedup window, schema generation (+ fuzz tests).
3. ✓ `KickerModule` library core: line I/O, hello/announce, ping/pong, events with ack/retry/dedup, log, identify (+ native tests). Verified on a Pico W.
4. ✓ `modules/session` + `kicker monitor` CLI. Verified on a Pico W.
5. ✓ Goal sensor firmware: `GoalGate` component (interrupt timestamps, `BeamDetector`: noise filter, lockout, stuck-ball fault). Bench-tested with IR gates.
6. ✓ `modules/`: serial discovery (VID allowlist, replug rule), adoption + roles, console buffer; virtual modules.
7. ✓ `store/`: migrations for settings, presets, modules, games, game_events; single writer; adoptions persisted.
8. ✓ `engine/` subset: manual start, goals, win check with presets, finished, live corrections (+1/−1/undo), restart recovery (+ scenarios).
9. ✓ `table/` actor + `bus/` + SSE hub.
10. ✓ Minimal auth: onboarding steps 1 + 3 only, login, sessions, groups with editable permissions (spec 03 §4), `Require(permission)` / `RequireAdmin`, admin API for groups, accounts and the audit log. Plain HTTP.
11. ✓ Web: setup wizard, login, scoreboard (live), modules page with console and adoption, groups & permissions editor, accounts list, simulator panel, custom "page not found". The UI hides what the account's permissions don't allow (`/api/auth/me`).
12. ✓ Bench rig test protocol and M1 acceptance checklist (spec 07); per-interruption diagnostics in the goal sensor firmware (0.4.0) and gate settings in the modules page. The rig itself and the tests with real balls continue in the hardware chat.

### M3: Players, coins, ratings
**Done when** (all in `kicker serve --sim`, coins and modules simulated):
1. Players with name, color and picture exist; the guest player is built in; accounts are linked to their player.
2. Inserting coins registers players on a side following spec 02 §4 (stale players, whole-side replacement, unknown coins → "assign this coin"); members can add and remove players by hand.
3. With both sides registered, the kickoff countdown runs and the game starts by itself; the roster is in the game's events.
4. Inactivity pauses the game and sends the table to sleep; a paused game can be resumed (smart wake or from the paused games list) or abandoned as in D16.
5. Finished games have Elo ratings (solo/team); editing a finished game (within the group's permission) replays the ratings; every edit is in the game's history.
6. Player profiles show wins/losses, rating history, best teammate and worst enemy; the overview page shows recent games and highlights below the scoreboard.
7. Accounts can sign up and be approved; password reset codes, the recovery key and the emergency kit work; the kicker serves HTTPS with its own CA.
8. `kicker loadtest` with 100k games meets the budgets of §4.

**Tasks:**
1. ✓ Players: table, guest, colors from the palette, pictures (resized, stored by content hash), account ↔ player link, players page + profile editing, permissions `players.create` / `profile.own` / `profile.any`.
2. ✓ Coins: table, assign / reassign / deactivate, guest coins (`coins.manage`); virtual coin acceptors in the simulator; `coin_acceptor@1` events into the table.
3. ✓ Registrations (spec 02 §4): `table_registrations`, rules 1–6, stale sources, unknown coins + assigning them, manual registrations (`table.players`), roster events in the game.
4. ✓ Table settings (spec 02 §13, D17): settings page for side names/colors and game defaults; kickoff flow with countdown, `kickoff_requires`, "Start now", first ball.
5. ✓ Pause, sleep, wake (spec 02 §9, D16): inactivity timers, module `sleep`/`wake`, manual pause/resume, paused games list (no queue since 2026-11-23), smart wake per side, abandon after hours + reinstate; coin eject at game end, pause and sleep.
6. ✓ Ratings (D9): Elo as a pure function (`ratings/`), `solo`/`team` keys, projection tables, replay from a given game, fixed guest rating.
7. ✓ Games: history list, game page with the event log, editing finished games (correction events, `games.edit` scope + days, revert, void), `stats_changed` over SSE.
8. ✓ Stats: player profile (wins/losses, rating chart, best teammate, worst enemy), leaderboard, overview page sections below the scoreboard.
9. ✓ Accounts, full spec 03: sign-up with registration modes, approval with group + player, reset codes, recovery key + emergency kit (onboarding step 4), "My devices".
10. ✓ HTTPS (spec 03 §9): local CA, server certificate with renewal, HTTP → HTTPS redirect, `/ca.crt` + help page, `__Host-` cookie.
11. ✓ `kicker loadtest` (100k games through the real engine, the budgets of §4, `--fresh`, `-v`) + Playwright end-to-end tests against `--sim` (setup wizard, a live game watched from a second browser, void, sign-up + approval, the simulated goal sensor; `make e2e`, CI job `e2e`). Decisions A–C in §4.
12. ✓ Spike: NFC tag reading on iOS/Android (spec 08). iOS background reading ignores custom schemes, so the tags hold an https link on the project's link domain `112233444.xyz` (decision B + D, 2026-11-25); the link site is in `linksite/`. The read-range test through the corner material moves to the corner hardware.

### M5: Ball dispenser
- Dispenser mechanism + firmware (release, release_all, exit sensor).
- Engine: first-ball and after-goal rules, ball tracking and "give me my ball back" (spec 02 §6.1).

### M6: Tournaments (D11, spec 09)
**Done when** (in `kicker serve --sim`): a tournament in every format can be set up from the duration-sorted options, played on the table – pairings start on check-in or "Play", in any order – paused and continued, and ends with placements; brackets and standings update live.

**Tasks** (spec 09 §14):
1. ✓ `tournament/` core: the six formats (round robin, groups → next stage crossed or re-seeded with optional best thirds, knockout with byes and 3rd place game or shared 3rd, double knockout with bracket reset, Swiss with byes and no rematches, Monster DYP with sit-out rotation), seeding, best of n, walkovers, ready pairings in any order + the suggestion, standings with the tie-breaks, placements, the duration estimate and the format options. Derived from setup + results; a property test plays every format to the end for 2–33 teams in random order, plus scenario files.
2. ✓ Store: migration 0011 (tournaments, participants, pairings once played, events; `games.pairing_a_side`), lifecycle moves with their events, walkovers, results per pairing (finished, non-voided games), delete only without games.
3. ✓ Table integration: engine tournament mode (coins check in, only a staged pairing kicks off, the game carries its pairing), the `tournaments` service (start with DYP draw and seeding, check-ins and "Play" stage pairings in any order, best-of games with swapped sides and first ball to the loser, finish, pause/continue, pause on sleep, restore after a restart).
4. ✓ API + permissions (`tournaments.*`, own = the creator), SSE topic `tournament` (routes in spec 09 §13).
5. ✓ Web: setup page with the format list, tournament page (bracket, groups, standings, ready pairings), scoreboard tournament mode, profile additions.
6. ✓ Changing a running tournament (spec 09 §10–11): replacing players, dropouts in every format (walkovers, annulled or forfeits, not drawn any more; a property test drops and replaces at random in every format), rename and rules of later stages, conflicts after edits and the rebuild, finished tournaments frozen and reopened by a void. No manual results: the table stands on its own.
7. ✓ Load: a 64-team double knockout and a 40-player Monster DYP played to the end in `kicker loadtest` (`--tournament-teams`, `--monster-players`; budgets in §4); e2e test of a small knockout from the setup page to the podium (`05-tournament.spec.ts`).

### M7: Operations (spec 10)
- Full onboarding wizard, encrypted backups + emergency kit, automatic backup before migrations, signed updates with rollback, firmware updates over USB, SD card image.
- Tasks and decisions in spec 10 §8–9. Done: 1 (backups: package `backup`, age with `filippo.io/age`, restore by restart, several locations, schedule and retention, the list with restore/download/delete and upload, before migrations, `kicker backup` / `kicker restore`), 2 (Settings → Backups, changing the passphrase).

### M8: Marketplace (D6)
- Manifest index in a git repo, firmware updates from it.
- **Generic firmware with a stored pin map** (§10, option 2): one prebuilt `.uf2` per board; the wiring is set in the module's page in the UI and stored in the module's flash.

### M2: Side displays (after M8)
- Display firmware on RP2350 + screen: state, animations (incl. variants), texts in flash, picture cache.
- Engine: display state mapping (spec 02 §10), translations + side names (D17), virtual displays in the simulator.

### M4: Phones (last)
- Tauri app: pairing (mDNS / URL / QR), pinned TLS client, accounts per kicker.
- Corner tags + claim flow; corner presence firmware.

**Link domain `112233444.xyz`** (registered until 2036, spec 08): what the corner tags point to.
- **Tag content:** `https://112233444.xyz/c?s=<server_id>&c=<corner>` (spec 03 §11). Corners as in spec 02 §5 (`home_1`, …).
- **Hosting:** Cloudflare Pages, static files only from `linksite/public/` (README there: deploy, settings, checks). No DNS record, tunnel or port ever points to a kicker.
- **App links:**
  - iOS: `.well-known/apple-app-site-association` lists `<Team ID>.<bundle ID>` for path `/c` with query items `s` and `c`; the app gets the Associated Domains entitlement `applinks:112233444.xyz`. Apple's CDN fetches the file, so it must be public, https with a valid certificate, no redirects.
  - Android: `.well-known/assetlinks.json` with the package name and the SHA-256 of the signing certificate (the Play App Signing key if distributed through Play); intent filter `https://112233444.xyz/c` with `android:autoVerify="true"`.
  - Both files hold placeholders until the app's IDs and signing key exist. Android package names can't have a segment starting with a digit, so the app ID isn't the reversed domain.
- **No app installed:** `/c` forwards in the browser to `https://kicker.local/claim?s=…&c=…`. The kicker gets a **`/claim` page**: checks that `s` is its own `server_id` (otherwise "This tag belongs to another kicker"), asks to log in if needed, then claims the corner (`POST /api/table/claims`, `table.join`).
- **The app** handles the link on both platforms (universal link / App Link), picks the kicker by `server_id`, and offers "Join" → scan for phones without background tag reading. "Program corner tags" writes the URL and can lock the tag.
- **Keep the domain:** it is printed on every table's tags. Renew it long before 2036; if it ever lapsed, someone else could serve the no-app page (not open the app – that needs our signing identity).

## 8. Decided in review (2026-11-18)
- Private repo `Humbarrt/kicker`; the owner initialises git.
- Target hardware: Raspberry Pi 4, 4 GB.
- Plain HTTP until M3; HTTPS in M3.
- Goal sensing: 2 IR break-beam sensors (Adafruit 2168, open collector, internal pull-up) in the tube below each goal that feeds the dispenser.
- JSON Schemas come from an own small generator instead of `invopop/jsonschema` (keeps `protocol/` dependency-free).
- `make generate` runs before dev, build and test.
- Wiring vs behaviour (§10): pins live in the firmware as component arguments, not in the server config.
- (2026-11-23) Build order software first: M2 (side displays), M4 (phones) and M5 (ball dispenser) move to the end; milestone numbers stay. Hardware is done in a separate chat; M1 task 12 is closed with the test protocol (spec 07).
- (2026-11-20) Web UI: translations in `web/src/lib/i18n.svelte.ts` (English source, German), the browser can override the kicker's language. The Content-Security-Policy is built by the Go server from the hashes of the inline scripts in `index.html`. The simulator panel sends virtual module events via `/api/sim` (only with `--sim`); the module console streams to admins over SSE (`console` topic).
- (2026-11-20) Plain HTTP until M3: the session cookie is `kicker_session` without `Secure` (browsers refuse Secure cookies over HTTP); it becomes `__Host-kicker_session` as soon as the request is HTTPS. `kicker admin create` also completes the setup wizard (physical access, spec 03 §7); `kicker admin set-password` stands in for console reset codes until M3. The common-password list contains the 10,000 most common passwords with at least 10 characters (shorter ones are refused by length anyway).
- (2026-11-23) Ratings: start 1000, K 32 for everyone, pair bonus +100 (team key), guest fixed at 1000 – starting values until real data (D9 open details). Projections (`games.home_goals/away_goals/winner`, `game_participants`) are written with every game write; `table.ProjectionsVersion` rebuilds them from the events once when it goes up. The rating projector (`stats/`) runs beside the table and announces changes over SSE (`stats`).
- (2026-11-24) Editing finished games: one correction event per edit (spec 02 §11), `reverted` events take one back (migration 0008 adds the type). Permissions: goals, players and sides need `games.edit` (scope own = games the account's player played in) within `games.edit_days`, counted from the game's end; voiding needs `games.void`, reverting `games.revert`. The feed of recent edits is shown to `games.revert` (Settings → Edits). Games are edited through the table actor, so a game still on the scoreboard updates too.
- (2026-11-24) **One "Settings" menu** in the header (route `/settings`) instead of Admin, Coins and Log. Its tabs are shown by permission: Table, Modules, Groups, Accounts for admins; Coins with `coins.manage`; Edits with `games.revert`; Audit log with `log.view` (IP addresses with `log.ip`). The header item is hidden when no tab is allowed.
- (2026-11-24) Stats (task 8) count rated games only (D9). Every rating row also stores the game's end, the player's result and goals (migration 0009); `player_ratings` carries the totals per key, so the leaderboard is one query. `stats.Version` replays everything once when the projections change. Profile: record, win rate, goals, current and longest run, form (last 10), rating per key with its history (chart, at most 300 points), teammates and opponents; **best teammate** = highest win rate together, **worst enemy** = the opponent who beats them most often, **favourite opponent** = the one they beat most often – each needs 3 games together, more games break a tie. Leaderboard on the Players page per key, a rank from 5 games on (fewer: listed below as "not ranked yet"), with the rating change of the last 30 days and the last 5 results; the profile shows the player's rank per key. Overview below the scoreboard: games and goals of the last 30 days, highlights (current win run ≥ 3, most games, biggest rating gain, biggest win), top 3 per key, latest games. Abandoned games don't count for goal stats yet (D9 allows it; open).
- (2026-11-24) Game list filters (`GET /api/games`, kept in the page URL so filtered lists can be linked): up to 4 players (all played), together/against for the first two, result for the first player (won/lost/draw), 1 against 1 / team, finished / abandoned, and "only" flags – void, edited, unknown coin, unknown (the guest, an unknown coin or nobody on a side), shutout (the losers scored nothing), close (won by one goal) – plus a date range. On wide screens the filters sit beside the list, always open; on phones above it, folded.
- (2026-11-24) Accounts (task 9): sign-up with the three registration modes (Settings → Accounts), approval with group and player (the requested new player is created on approval), reset codes from admins and `kicker admin reset-code`, the recovery key (12 BIP39 words, argon2id hash) and the backup passphrase (8 words, `backup.passphrase` in the data folder) in one emergency kit – onboarding step 4, again in Settings → Security (always a new key). "My devices" on the account page. Admins see sign-ups and a missing kit as badges (SSE topic `accounts`).
- (2026-11-24) HTTPS (task 10): package `tlsca` makes the CA and the server certificate in the data folder (`ca.key`, `ca.crt`, `server.key`, `server.crt`, mode 0600), checks names, addresses and expiry every 10 minutes and swaps the certificate without a restart. `--tls-addr` turns it on; without it the server stays plain HTTP for development. The CA fingerprint is in `/api/info`, Settings → Security, the setup wizard and the emergency kit.
- (2026-11-24) The goal sensors don't wake the table: they are off while it sleeps. Wake sources are coins, phone claims and the UI.
- (2026-11-19) SQLite driver `ncruces/go-sqlite3` instead of `modernc.org/sqlite` (the alternative named in D2); own migration runner instead of goose; hand-written queries instead of sqlc for now; `synchronous=NORMAL`.

## 9. Building the table and the modules side by side
The table (cast terrazzo) and the modules are designed at the same time, so neither can wait for the other. Three things keep them decoupled:

1. **Software never waits for hardware.** The simulator (§3) runs every capability, so the server, the web UI and the display screens can be built and tested completely before any module exists.
2. **Modules are developed on a bench rig, not in the table.** A simple test stand (plywood or 3D-printed) holds:
   - a section of the goal tube with the IR gate
   - later: a coin acceptor, a corner with tag + distance sensor, the dispenser with a short tube stub, the two displays
   - a USB hub and the Pi
   Every module is finished and tested on the rig, then moved into the table unchanged.
3. **The table only commits to interfaces, not to module internals.** A separate hardware spec (`05-hardware-interfaces.md`, written in the furniture/3D chat) fixes, per module:
   - the **bay**: maximum size, position, how it's fastened (e.g. cast-in threaded inserts), and how it's reached for service (from below, without lifting the table)
   - **fixed dimensions** the modules must match: ball tube inner diameter and slope, coin slot size, corner pocket size, display window size
   - **cable channels** from each bay to the Pi/hub location, with spare capacity
   - **power**: everything from the powered USB hub (5 V), budget per bay

   Because terrazzo can't be changed after casting, the bays should be **oversized**, with 3D-printed adapter frames inside them. A module can then be redesigned many times without touching the stone.

## 10. Module wiring vs behaviour
Two kinds of module configuration are kept apart:

| | Wiring | Behaviour |
|---|---|---|
| Examples | which pin a gate is on, beam polarity, display bus pins | lockout time, noise filter, display texts, side names |
| Changes | only when someone re-solders | any time |
| Lives in | the module | the server |
| Set via | firmware (now), module flash (M8) | `cfg` messages (spec 01 §4) |

Reasons the server doesn't push wiring: the module must announce its capabilities before it gets any config; a wrong output pin can damage hardware; the server would need every board's pin layout; a bad value could make a module unreachable.

- **Now – composable firmware:** the library provides **components** (ready-made capabilities that drive hardware, e.g. `GoalGate("a", pin, brokenLevel)`). A module's `main.cpp` is just a list of components with their pins; combining capabilities on one board means adding components. The library calls each component's `begin()` / `update()` hooks. More components follow with their milestones (coin slot, corner sensor, dispenser, display).
- **Later (M8) – generic firmware with a stored pin map**, Tasmota/ESPHome style: a prebuilt `.uf2` per board; the wiring is set in the module's page (or `kicker monitor`) as module-level settings stored in the module's flash; the module reboots and announces the matching capabilities. Wiring still stays on the module, never in the game config.
