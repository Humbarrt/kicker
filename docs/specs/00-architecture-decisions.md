# Kicker v3: architecture decisions

Status: living document. **Decided** = agreed with the owner. Last update: 2026-11-23 (D16: no queued games).

## Guiding principle
Build one complete path first (goal sensor → server → live score on the side displays + web), then build outward. Both earlier attempts stalled on infrastructure before a single game could be played.

## Spec documents
1. Module wire protocol + capability catalog → `01-module-protocol.md`
2. Game engine: registrations, kickoff, goals, ball tracking, pause/sleep, event model → `02-game-engine.md`
3. Auth, pairing, encrypted backups, onboarding → `03-auth-pairing-onboarding.md`
4. Repository layout, tooling, milestones → `04-repo-and-milestones.md`

---

## D1. Server: one Go binary on a Raspberry Pi (**Decided**)
- One process: **gin** HTTP router, game engine, module host, SQLite.
- SvelteKit build (`adapter-static`) is compiled into the binary with `embed`.
- Updates to the UI and table are pushed over **SSE** from an in-process event bus.
- Pi uses an SSD or a high-endurance SD card. Outbound internet access is allowed. The server is never exposed to the internet.

## D2. Database: SQLite (**Decided**)
- WAL mode, one writer goroutine, pure-Go driver, goose migrations, sqlc.
- Games are stored as an **append-only event log**. Game state and all statistics are *projections* and can be rebuilt.
- Fixed, well-known fields are real columns with CHECK constraints. JSON is only used for open-ended data (module config, plugin data).

### Editing games after the fact
- Edits append correction events (`goal_voided`, `goal`, `player_joined`/`player_left`, `sides_swapped`, `rules_changed`, `game_voided`, …) with author, reason and time. Nothing is changed or deleted.
- The event log is the audit log. Each game shows its edit history. Admins see a feed of recent edits and can revert one.
- After an edit: rebuild the game's projection → replay ratings from that game on → publish `stats_changed` over SSE.
- Load test includes editing the oldest game when 100k games exist.

### Goal timing
- Sensor goals carry `play_time_ms` (pauses removed; module clock offsets corrected).
- `ball_released` events are logged, which allows "time from ball release to goal".
- Manual goals are excluded from timing statistics. Goals are credited to the team.
- No shot speed: the light gate sits in the pipe below the goal.

### Players and slots
- Each team has 2 slots, filled in registration order. No positions. A corner tag only identifies the side.
- **An empty slot means nobody played there.** The format (1v1, 2v1, 2v2) follows the number of registered players. A side with nobody registered → the game is not rated.
- **Guest:** there is a special guest player, available in the player picker and via dedicated **guest coins**. It may fill several slots of the same game. It has a fixed rating that never changes.
- Players have an optional profile picture (resized on upload, stored by content hash, included in backups, small versions pre-rendered for the side displays).
- Player color is the fallback, picked from a curated palette as one of the least-used colors.
- Registration rules (stale players, whole-side replacement, phones, unknown coins): spec 02 §4–5.

### Coins
- Coins are separate from players. A player can have 0..n coins.
- Lookup table `coins(uid → player_id)`, using the coin's factory NFC UID. Guest coins map to the guest player.
- A coin can be registered via the phone or by inserting it into a slot. An unknown inserted coin shows "unknown coin – assign?".
- Coins can't be pulled out by hand. The acceptor ejects all its coins at once, either automatically (game end, pause, sleep) or via its physical button.

### Rough tables
- `players` (uuid, name, color, avatar_hash NULL, is_guest)
- `coins` (uid, player_id, label, active, created_at)
- `accounts`, `sessions`, `reset_codes`, `recovery_key`, `audit_log` (spec 03 §14)
- `rule_presets` (id, label, win_score, win_by, max_score NULL, time_limit_s NULL, time_tie, is_builtin)
- `games` (id, preset_id NULL, the rule columns copied in, pairing_id NULL, state, started_at, ended_at, next_ball_side)
- `game_events`: columns only, no JSON (spec 02 §11)
- `table_registrations` (side, slot, player_id NULL, coin_uid NULL, sources, status)
- Projections: `game_participants` (game_id, side, slot, player_id), `goals`, `ratings` (game_id, player_id, rating_key, before, after), `player_stats`, `pair_stats`
- `modules`, `module_capabilities`
- `settings` (incl. `current_preset_id`, side names/colors, language, `balls_out`, onboarding_done)
- `translations_override` (lang, key, text)
- Tournaments: see D11

## D3. Auth (**Decided**, details in spec 03)
- **Opaque session tokens, no JWT.** 256 random bits; the DB stores only the SHA-256 hash.
  - Web: cookie `__Host-kicker_session` (`Secure; HttpOnly; SameSite=Strict`).
  - App: `Authorization: Bearer`.
  - Sessions can be revoked.
- Passwords hashed with argon2id. Login attempts are throttled.
- HTTPS with a CA created by the Pi on first boot. The app pins the CA.
- Roles (full permission table: spec 03 §4):
  - **Anonymous / pending:** read-only. Pending accounts can log in and see "Waiting for approval".
  - **Member:** admin-approved and linked to a player.
    - Can do everything at the table, manage coins, create players, run tournaments, and change the win conditions of the current/next game.
    - Can edit games from the last N days (default 7). Needed for joining via phone.
    - Can't create or edit presets.
  - **Admin:** everything, including presets, settings, modules, accounts.
- **Every change through the web or app requires at least a member login.**

## D4. Web + companion app (**Decided**)
- One SvelteKit codebase for the browser and a Tauri v2 app.
- Default URL `https://kicker.local`, with an mDNS lookup fallback.
- No offline caching. If unreachable: "Server not found, edit URL?". Accounts are stored per `server_id`.
- In the app, all API traffic goes through the Rust side so the kicker's CA can be pinned (spec 03 §10).

## D5. Module bus: USB-CDC, capability model (**Decided**)
- Microcontrollers connect over USB-CDC through a powered hub, behind a transport interface.
- One microcontroller can host one or many capabilities: `goal_sensor@1`, `coin_acceptor@1`, `corner_presence@1`, `ball_dispenser@1`, `display@1`, or a third-party `vendor.name/thing@1`.
- Modules only report events and carry out commands. All rules live in the game engine.
- Full protocol and catalog: `01-module-protocol.md`.

## D6. Plugins / marketplace (**Decided, later**)
1. Third-party modules built on standard capabilities ship only a manifest.
2. A git repo holds the index of manifests, like HACS. Firmware updates are sent over USB.
3. Custom server logic runs as WASM plugins via `wazero`.

## D7. Module discovery + handshake (**Decided**)
- The server checks the list of serial ports every 2 s. DTR/RTS are set on purpose when opening a port.
- **Which ports are probed automatically:** USB vendor IDs on a broad default allowlist (Raspberry Pi, Espressif, Arduino, Adafruit, Seeed, SparkFun, STM, CH340, CP210x, FTDI, …).
  - Any other serial port shows up in the UI as "unknown serial device → probe / ignore", and the user can add vendor IDs.
- `hello` → `announce` within 500 ms. A port that doesn't answer validly is skipped until it is unplugged and reconnected.
- Known uid → server pushes the stored config. Unknown uid → "New module discovered → adopt".
- Heartbeat every 1 s (also measures the clock offset). Three missed → offline.

## D8. Firmware + wire format (**Decided**)
- Arduino C++ (PlatformIO) for reference firmware, plus a `KickerModule` library.
- The library only depends on the Arduino `Stream` (Serial) and `millis()/micros()`, so it runs on any board with an Arduino core and USB serial.
- Wire format: newline-delimited JSON, short keys, max 1 KB per line, with a protocol version.
- Server-side debugging:
  - schema validation for every message
  - `{"t":"err"}` replies, so errors show up in the module's serial monitor
  - a live module console in the web UI
  - a `kicker monitor <port>` CLI that acts as a fake server
  - `log` messages from modules

## D9. Ratings (**Decided**)
- The rating algorithm is a pure function over the ordered game log, behind an interface.
- Elo, with the expected result computed per team: team strength = average of its players' ratings + a team-size offset (solo vs pair). Teammates get the same change.
- Rating keys: `solo` (1v1) and `team`.
- **What counts:**
  - free-play games
  - tournament games, including those of abandoned tournaments
- **What doesn't count:**
  - forfeits and byes
  - games with an empty side
  - abandoned free-play games (no result; their goals still count for goal statistics)
- Open details: K-factor (higher K for new players), whether the goal difference scales the change. To be decided with real data.

## D10. Game rules + presets (**Decided**)
- Rules are columns. A game **copies** the preset's values when it starts, so editing a preset never changes old games.
- **A game is always a single game.** Its win condition depends only on itself.
- Win conditions:
  - `win_score`: first to N
  - `win_by`: required lead
  - `max_score` (optional): hard cap. Target = min(max(win_score, lower_score + win_by), max_score), as in v1.
  - `time_limit_s` (optional): when time runs out, the team that is ahead wins. If it's tied → `time_tie` = `golden_goal` (default) | `draw`.
- **Tournament games can never end in a draw:** `time_tie` is always `golden_goal` there.
- Built-in presets: Long 10/2/15, Short 6/1/6, Official 5/2/8, plus "Timed" (5 min, golden goal). Admins create and edit presets.
- Members can change the win conditions of the current or next game (a copy; logged as `rules_changed`).
- **Sticky preset:** the preset used for a free-play game becomes the table's current preset, so the next game uses it automatically.
  - Tournament games use their stage rules and don't change the current preset.
  - After a tournament, free play goes back to the previous preset.
- Which side gets the ball (first ball, after a goal) is a table setting: spec 02 §6–7.

## D11. Tournaments (**Decided**)
- **Time first.** Setup shows every format option for the current number of teams, **sorted by estimated duration**. For each option it shows:
  - number of games
  - estimated total time
  - minimum games per team
- The estimate uses: (time limit, or the real average game length) + changeover time, ÷ number of tables.
- Default tournament rules: time limit + golden goal. No draws.
- Formats: single knockout, double knockout, groups → knockout, Swiss (→ optional final/knockout), round robin.
- Team building: fixed teams, **DYP** (teams drawn at random once), or **Monster DYP** (new random partner every round, individual ranking).
- **Pairings:** a pairing is one entry in the bracket or table (team A vs team B).
  - A pairing is made of **1..n independent games**. Each game is a normal single game: rated on its own, with its own stats.
  - The tournament simply shows a pairing's games as one row (e.g. 2:1).
  - Configured per stage: "games per pairing" = 1 (default) | best of 3 | best of 5. Example: final best of 3.
  - **Double knockout grand final:** the winner of the winners bracket has not lost yet. If the team from the losers bracket wins the first final game, a second deciding game is played ("bracket reset").
- The tournament can decide which team gets the first ball of each game.
- **Odd numbers of teams:**
  - Knockout: the bracket is filled up to the next power of 2 with **byes**. The best-rated teams get them (seeding by Elo).
  - Groups: unequal group sizes are allowed (e.g. 3 + 4). Group rankings use points per game played.
  - Swiss: one bye per round, rotating. A bye counts as a win in the standings but not for ratings.
  - **Odd number of players** for 2v2: one solo team, or one player sits out each round in rotation (Monster DYP).
- Placements: 1st, 2nd, and 3rd via a third-place game or shared 3rd (setting). Configurable tie-breaks.
- **Teams are separate from their players.**
  - Replacing a player applies from the next game on.
  - Dropouts: in knockout, the opponent advances by walkover. In groups, if fewer than 50% of the team's games were played, they are annulled; otherwise the rest are forfeits.
  - Forfeits never count for ratings.
- **Tournament lifecycle:** `setup → running → paused → running | finished | abandoned`.
  - When the table sleeps during a tournament, the tournament is **paused** together with its current game. On wake, the table offers to continue it.
  - Paused tournaments appear in a list and can be **picked up** later. Their current game resumes as in D16, then the next ready pairing follows.
  - A tournament only becomes `abandoned` through an admin, or after a long timeout (`tournament_abandon_after_days`, default 7).
  - Abandoned tournaments can be reinstated by an admin.
- **Abandoned tournament:** games count for ratings and stats, but nobody wins.
- Player profile shows tournament stats: tournaments played, won, podiums.
- Editing a result that changes who advanced, after a dependent game has started → needs an admin's "rebuild bracket from here".
- Tournaments are event-sourced (`tournament_events`). The table shows the next ready game and starts it when the expected players register.
- Rough tables: `tournaments`, `tournament_stages`, `tournament_teams`, `tournament_team_members` (with valid_from), `pairings` (stage, round, slot, team_a, team_b, games_to_win, winner), `tournament_events`.

## D12. Side displays (**Decided**)
- Two small LCDs, one per side: score, the side's players (picture or color + name), animations for goal, win, loss and shutout ("destruction").
- Each is one `display@1` instance (`home`/`away`).
- The module draws the screen. The server sends `state{...}` and `anim{name}`. Pictures are sent once and cached by hash.
- A display may have several variants per animation and picks one at random.
- All text on the displays is translatable and sent with the display's config (D17).
- Optional TV via HDMI = the web scoreboard page in a full-screen browser.
- Candidate screens: 240×240 ST7789 or round GC9A01, with LVGL/TFT_eSPI.

## D13. Reference boards (**Decided**)
- "Reference" only means the boards we build, test and document with. Builders can use any board that has an Arduino core and USB serial.
- Simple modules: **RP2040-Zero** (no radio, native USB, custom USB descriptors via TinyUSB, UF2 updates over USB).
- Display modules: **RP2350** (520 KB RAM). Alternative: ESP32-S3 with PSRAM.

## D14. Backups (**Decided**)
- `VACUUM INTO` snapshot + pictures + identity files → `.tar.gz`, **always encrypted with age** using the backup passphrase (spec 03 §8).
- Default daily, keep the last N. Destinations: local folder / USB stick, SMB/NFS; WebDAV/S3 later.
- Automatic backup before updates and DB migrations.
- Download and restore in the UI (file + passphrase). Restore is also offered at first boot (= moving to a new Pi, keeping the kicker's identity).

## D15. Updates (**Decided**)
- Updates come from signed GitHub releases (ed25519/minisign).
- The release repo and public key are compiled in by the official CI, so forks have updates disabled by default. A fork can set its own repo and key.
- Install steps: backup → download + verify → swap the binary (keep the previous one) → restart via systemd + health check → roll back on failure.
- Module firmware updates are part of the same flow (over USB).

## D16. Game lifecycle: pause, resume, abandon (**Decided**, details in spec 02 §7.4, §9)
- States: `active → paused → active | finished | abandoned`, plus `discarded`.
- **Pause after inactivity:** with no goal and no table activity for `pause_after_minutes`, the game becomes `paused` (backdated to the last activity) and the table goes to sleep.
- **Manual pause (members):** resumes on the next goal (which counts) or via "Resume".
- **"Start new game" (members):** pauses the running game (or discards it if it has no goals). The UI never abandons games.
- **Paused games list:** every paused game is listed (score, players, age, why it was paused) and can be resumed by hand.
- **Wake:** `on_wake = smart (default) | resume | new_game`, for the most recently paused game, **only as long as no new game was started since it was paused**:
  - `smart` resumes it once at least one of its players per side is back (coin or phone); until then the normal kickoff flow goes on, and a new game that starts first wins.
  - `resume` always continues it.
  - `new_game` never does.
  - Once a new game has started, a paused game only comes back by hand from the list.
- **Resuming a paused game** while another game is running:
  - If the running game has **no goals**, it is **discarded**: removed completely, never counted.
  - If it has goals, it is paused and goes to the paused games list. **Nothing continues by itself** when the resumed game ends (no queue; changed 2026-11-23).
- A resumed game keeps its own players. The current table registrations are kept for the game after it.
- **Abandon:** a paused free-play game becomes `abandoned` after `abandon_after_hours` (default 24 h). Members (within their edit window) and admins can reinstate it, which makes it `paused` again.
- Tournament games don't time out to `abandoned` on their own. They follow their tournament (D11).

## D17. Sides, display names, language (**Decided**)
- Internally the two sides are always `home` and `away` (code, DB, protocol).
- Each side has a configurable **display name** and **color** in the table settings, e.g. "Red Team" / "Blue Team", to match the table's team colors. The web UI, the app and the side displays all use these.
- **Translations:** every text shown to users (web, app, displays) is translatable.
  - The server ships language files. Users can override any individual text in the settings.
  - Displays receive their language's texts and the side names through their config, and keep them in flash so they can show texts even without a server connection.

## D18. First-boot onboarding (**Decided**, details in spec 03 §12)
- On first boot the web UI shows a step-by-step setup wizard, like Home Assistant. Until it's finished, every other API endpoint is locked.
- **Onboarding never depends on hardware.** Only steps 1–4 are required; everything else can be skipped and done later.
- Steps:
  1. Language
  2. Start fresh or restore an encrypted backup
  3. Admin account
  4. **Emergency kit**: recovery key (12 words) + backup passphrase, as one downloadable file
  5. Table: side names + colors
  6. Game defaults
  7. Modules (with the live module console)
  8. Backups + updates
  9. Done: QR code for pairing the app
- As in Home Assistant, whoever starts the wizard first becomes admin (accepted on a local network). No setup code.

## Open
- Rating details: K-factor, goal difference (can be decided with real data later).
