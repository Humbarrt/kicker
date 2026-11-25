# Spec 01: Module protocol + capability catalog

Status: **Approved v5** (2026-11-18). Builds on D5, D7, D8, D12, D13, D17 in `00-architecture-decisions.md`.
Protocol version: **1**

Changes in v5 (found by the end-to-end test of library + server in M1 task 4): `announce` carries `ts`; a repeated announce of a running module no longer counts as a reboot; the first ping goes out right after the handshake.

Changed 2026-11-24: goal sensors are no longer a wake source – their gates are off while the table sleeps (§10 `goal_sensor@1`, spec 02 §9).

## 1. Goals
- Anyone with an Arduino-compatible board can build a module, debugging with nothing but a serial monitor.
- No lost goals. Every event is acknowledged and deduplicated.
- The server owns all game logic. Modules report what happened and do what they're told.
- One microcontroller can host any number of capabilities. A capability doesn't care which board it runs on.

## 2. Transport and framing
- USB-CDC (or a USB-UART bridge at **115200 baud**, 8N1). On native USB the baud rate is ignored.
- **One message = one line**: a UTF-8 JSON object followed by `\n` (`\r\n` is accepted).
- Max line length is **1024 bytes** including the newline. Longer lines are dropped, answered with `err too_long`, and appear in the module console.
- **Lines that don't start with `{` are treated as raw log output.** They show up in the module console as warnings but are never errors, so a stray `Serial.println("debug")` breaks nothing.
- Receivers ignore unknown fields. The server additionally logs a warning in the module console.
- No pretty printing, no comments, no trailing commas.

## 3. Common fields
| Key | Meaning |
|---|---|
| `t` | message type, required |
| `i` | capability instance name (unique within a module). Missing = the message is about the whole module |
| `id` | command id (server → module), uint32, echoed in `ack`/`nack`. Wraps from 4294967295 to 0 |
| `n` | event sequence number (module → server), uint32, starts at 0 after boot. **Wraps from 4294967295 to 0** |
| `ts` | module time in **µs since boot**, as a 64-bit integer. The library handles `micros()` overflow |
| `d` | payload object (event data, command args, config) |

**Wrap-around rule:** `id` and `n` are only ever compared for *equality* within a small recent window, never with `<` or `>`.
- The server keeps the last 64 `n` values per module session to detect duplicates. The module keeps the last 8 `id`s.
- A wrap from 4294967295 to 0 is therefore harmless.
- A reboot is detected by the module's uptime going back (§5), not by `n` going back to 0.

## 4. Message types
### Server → module
| `t` | Fields | Module must answer |
|---|---|---|
| `hello` | `v` (max supported), `vmin`, `st` (server time µs) | `announce` |
| `ready` | `sid` (session id), `adopted` (bool) | – |
| `ping` | `id` | `pong` |
| `cfg` | `id`, `i`?, `d` | `ack`/`nack` |
| `cmd` | `id`, `i`, `c` (command name), `d`? | `ack`/`nack` |
| `ack` | `n` (acknowledges an event) | – |
| `sleep` / `wake` | `id` | `ack` |
| `identify` | `id`, `d:{s}` (seconds) | `ack`; then blink an LED / beep for `s` seconds |
| `reboot` | `id` | `ack`, then reboot |
| `update` | `id` | `ack`, then enter the bootloader (see §9) |
| `err` | `code`, `msg`, `ref`? | – |

### Module → server
| `t` | Fields | Server answers |
|---|---|---|
| `announce` | see §5 | `ready`, then `cfg` |
| `pong` | `id`, `ts` | – |
| `evt` | `i`, `e` (event name), `n`, `ts`, `d`? | `ack{n}` |
| `ack` | `id` | – |
| `nack` | `id`, `code`, `msg`? | – |
| `status` | `i`?, `s` = `ok`/`degraded`/`fault`, `msg`? | – |
| `log` | `l` = `d`/`i`/`w`/`e`, `m` | – |
| `err` | `code`, `msg`, `ref`? | – |

### Config is a partial update
- A `cfg` message **merges** its keys into the instance's current config instead of replacing it.
- The server can therefore split a large config (e.g. display texts) over several `cfg` lines to stay under 1 KB.
- After every `ready`, the server sends the full config again.

## 5. Announce
```json
{"t":"announce","v":1,"uid":"e66141040b3c2a29","ts":1532114,"fw":{"name":"kicker-goal-sensor","ver":"1.2.0"},"mf":"kicker/goal-sensor","hw":"rp2040-zero","upd":"uf2","caps":[{"i":"a","c":"goal_sensor","cv":1},{"i":"b","c":"goal_sensor","cv":1}]}
```
| Key | Meaning |
|---|---|
| `v` | protocol version chosen by the module (≤ server `v`, ≥ server `vmin`) |
| `uid` | stable unique id: RP2040 flash unique id, ESP MAC, … As a fallback, the library generates a random id once and stores it in flash |
| `ts` | module time in µs since boot, when the announce was sent |
| `fw` | firmware name + semver |
| `mf` | manifest id (`vendor/name`), links the module to its marketplace manifest |
| `hw` | free-text board name (shown in the UI) |
| `upd` | update method: `uf2` / `esptool` / `none` |
| `caps` | up to 16 entries: `i` instance, `c` capability, `cv` capability version, `p` optional static params, `w:1` if it can wake the table |

- The module sends `announce` in response to `hello`, **and once on its own right after booting**. The server therefore often receives two announces in a row (boot + reply to `hello`), and a board with a USB-UART bridge can reboot while the port stays open.
- **Every announce is answered with `ready`** (and the full config). Whether it starts a **new module session** depends on `ts`:
  - **Reboot** (first announce, a different `uid`, or `ts` lower than the latest module time the server has seen): new session. The dedup window and the clock offset are reset.
  - **Repeated announce** of the running module (`ts` not lower): same session. The dedup window is kept, so events the module resends after the second `ready` are still recognised as duplicates.
- If two connected modules announce the same `uid`, the second one gets `err duplicate_uid` and is ignored. This happens with copied firmware that has a hard-coded id.
- If a capability version isn't supported by the server, that capability is marked "unsupported" in the UI. The rest of the module still works.

## 6. Session flow
```
server: (port opened, input flushed)
server → {"t":"hello","v":1,"vmin":1,"st":1731920000000000}
module → {"t":"announce", ...}                         within 500 ms, else the port is skipped until replugged
server → {"t":"ready","sid":"k3f9","adopted":true}
module → (state sync events, see below)
server → {"t":"cfg","id":1,"i":"a","d":{"lockout_ms":1000}}
module → {"t":"ack","id":1}
server → {"t":"ping","id":2}                              right away, then every 1 s
module → {"t":"pong","id":2,"ts":5321443}
module → {"t":"evt","i":"a","e":"goal","n":0,"ts":9012345}
server → {"t":"ack","n":0}
```
- **State sync:** right after `ready`, every capability that holds state reports its current state as a normal event (see the catalog: `coin_acceptor` → `held`, `corner_presence` → `present`/`absent`). After a server restart or a reconnect, the server therefore knows which coins are inside and which phones are lying on the corners, without anyone touching the table.
- **Unadopted modules** get `ready{adopted:false}`. Their events are acknowledged and shown in the UI, which helps identify a module ("trigger the sensor now"), but they never affect a game.
- **Heartbeat:** the server sends the first `ping` right after the handshake, then every 1 s. After 3 missed `pong`s the module is marked offline.
  - A module that receives no `ping` for 5 s considers itself orphaned. It keeps unacknowledged events (no retries while orphaned) and resends them after the next `ready`. Displays show the cached "no server" text, the dispenser keeps its balls.
  - Heartbeats continue while sleeping.
- **Clock sync:** for each ping/pong the server calculates `offset = (send + recv)/2 − ts` and keeps the offset of the ping with the lowest round-trip time among the last 16. Event server time = `ts + offset`. Expected accuracy is about 1 ms (USB full-speed uses 1 ms frames).

## 7. Reliability rules
- **Events:** the module keeps every `evt` until it gets `ack{n}`. With no ack after 200 ms it resends with the same `n`, up to 5 times, then logs an error.
  - The server acknowledges **every** `evt` that is valid JSON – also duplicates and invalid ones – so the module stops resending. Invalid ones additionally get an `err` with `ref` = `n`.
  - The server deduplicates using the last 64 `n` values of the module session (see §3, §5).
  - Before the session is ready, the library queues up to 8 events; if more arrive, the oldest is dropped and a warning is logged.
- **Commands:** the server waits 500 ms for `ack`/`nack` and resends with the same `id` up to 2 times. The module remembers the last 8 command ids and answers a repeat with the same result, without executing it again.
- `ack` only means "accepted". If a command takes time, the module reports completion as an event (e.g. dispenser `released`).
- `err` replies are limited to 5 per second per port.

## 8. Errors
| `err` code (either side) | When |
|---|---|
| `bad_json` | the line starts with `{` but isn't valid JSON |
| `too_long` | the line is over 1024 bytes |
| `unknown_type` | unknown `t` |
| `unknown_instance` | `i` isn't announced |
| `unknown_event` | event name not in the capability schema |
| `schema` | fields missing or of the wrong type (`msg` names the field) |
| `proto_unsupported` | no common protocol version |
| `duplicate_uid` | see §5 |

| `nack` code (module) | When |
|---|---|
| `unknown_command` | command not supported |
| `bad_args` | arguments invalid |
| `busy` | can't do it right now, retry later |
| `hw_fault` | hardware problem (also send `status fault`) |
| `not_ready` | e.g. still initialising |

## 9. Power and updates
- `sleep`: the module turns off displays and LEDs, detaches servos, switches NFC readers to low-power polling. It stays connected and keeps answering pings.
- Capabilities with `w:1` keep watching their wake source and simply send their normal event (e.g. coin `inserted`). The server decides to wake the table and sends `wake` to every module.
- `update`: the module acks and enters its bootloader.
  - `uf2`: the server waits for the RP2 boot drive and copies the `.uf2` file.
  - `esptool`: flashed over the same serial port.
  - The UF2 flashing process itself is defined in a later spec.

## 10. Capability catalog v1
Every capability's schemas (events, commands, config, params) are published as JSON Schema files in the catalog folder of the repo, generated from the server's Go types. During adoption, each instance gets a **role**, and the server works only with roles.

`home` / `away` are the internal side names everywhere. What players see (e.g. "Red Team" / "Blue Team") comes from the table settings (D17) and is sent to displays in their config.

### `goal_sensor@1`
A light gate (or any sensor) detecting a ball that went into one goal. In the reference build it sits in the pipe below the goal, so it only detects that a ball passed, not its speed.
- **Role:** `home_goal` / `away_goal` (whose net the ball went into). Only one instance per role.
- **Wake source:** no. The gates are off while the table sleeps, so a ball dropped into a goal can't wake it (spec 02 §9). A goal sensor announcing `w:1` is not treated as a wake source; a trigger that still arrives while the table sleeps only returns the ball.
- **Events:**
  - `goal` (no data). `ts` = the moment the beam was broken. Whether it counts as a goal or only returns the ball is decided by the server (spec 02 §3, §6.1).
- **Config:**
  - `lockout_ms` (default 1000): ignore new triggers after a goal
  - `min_block_us` (default 300): shorter interruptions are treated as noise
- **Status:** `fault` if the beam stays blocked for more than 10 s (ball stuck in the pipe); back to `ok` when it clears.

### `coin_acceptor@1`
Holds the NFC coins of one side.
- **Role:** `home` / `away`.
- **Params:** `p:{cap:2}` = how many coins it can hold.
- **Wake source:** yes (`w:1`).
- Coins can't be pulled out by hand. They only leave by ejecting, and **only all at once**.
- **Events:**
  - `held` → `d:{uids}`: the coins currently inside. Sent as state sync right after `ready` (may be an empty list).
  - `inserted` → `d:{uid}`. `uid` = tag UID as a hex string.
  - `ejected` → `d:{by, uids}`. Sent after every ejection. `by` = `cmd` (the server ordered it) or `button` (the physical eject button on the acceptor). `uids` = the coins that were released.
- **Commands:**
  - `eject` (no arguments): eject all coins. Completion is reported as `ejected` with `by:"cmd"`.
- How the game engine uses these events: spec 02 §4.

### `corner_presence@1`
A distance sensor in one corner that detects whether a phone is lying there. The NFC tag in the corner is passive and not part of the module.
- **Role:** `home_1`, `home_2`, `away_1`, `away_2`. This matches the corner id stored on that corner's NFC tag.
- **Events:**
  - `present` (phone placed)
  - `absent` (phone lifted)
  - Both are debounced on the module. The current one is sent as state sync right after `ready`.
- **Config:** `threshold_mm` (default 15), `debounce_ms` (default 500).
- How the game engine uses these events (corner claim flow): spec 02 §5.

### `ball_dispenser@1`
Holds balls in the middle of the table and releases one to either side on command. Balls that go into a goal return to the dispenser through the pipe, so it refills itself. **The dispenser doesn't track how many balls it holds**; the server tracks how many are on the field (spec 02 §6.1).
- **Role:** none (one per table).
- **Params:** `p:{sense:true}` = it has an exit sensor that sees a ball actually leave. Strongly recommended; `release_all` and `empty` depend on it.
- **Commands:**
  - `release` → `d:{side, count?}`. `side` = `home` / `away` (which team's half the ball rolls to); `count` defaults to 1.
  - `release_all` → `d:{side?}`. Empties the dispenser, e.g. to swap ball size or material. The module keeps releasing until a release attempt produces no ball, then reports one `released` with the total `count`. Without `side`, the module alternates sides.
    - Without an exit sensor, the module runs a fixed number of cycles set in its manifest config.
  - `home`: move the mechanism back to its start position.
- **Events:**
  - `released` → `d:{side, count}`. With `sense`, sent when the exit sensor sees the ball(s); without it, right after the movement. Its `ts` is the start time for "release → goal" timing.
  - `empty` → `d:{side}`. Only with `sense`: the mechanism moved, but no ball came out within 500 ms. Either there's no ball left or one is stuck; the module can't tell which, so the UI says "dispenser empty or jammed". (After `release_all`, this is the expected end and is *not* reported as `empty`.)
- **Status:** `fault` if the mechanism itself fails (e.g. it can't reach its start position).
- **Speed:** the server sends `release` the moment it gets a `goal` event, so the next ball is on the table before the goal ball would have rolled back down a slope.
- **Which side gets the ball** is decided by the server, not the module (spec 02 §6–7): e.g. random first ball, the team with the lower rating, a team set by the tournament, the conceding team after a goal.
- Mechanism settings (servo angles, timings) are vendor-specific config in the module's manifest, not part of the catalog.

### `display@1`
One small screen showing one side's view.
- **Role:** `home` / `away`. Several instances per role are allowed (mirrors).
- **Params:**
  - `w`, `h`, `shape` (`rect`/`round`)
  - `img` (picture size in px, e.g. 64; 0 = no pictures)
  - `anims` (list of supported animation names)
- **Config** (partial updates, see §4):
  - `team` → `{name, col}`: this side's display name and color, e.g. `{"name":"Red Team","col":"#C0392B"}`. `opp` holds the same for the other side.
  - `lang`: language code, e.g. `de`
  - `txt`: translated texts, as key → string. Standard keys:

    | Key | English default |
    |---|---|
    | `paused` | Paused |
    | `golden_goal` | Golden goal |
    | `waiting` | Waiting for players |
    | `kickoff_in` | Kickoff in |
    | `return_ball` | Put the ball in a goal |
    | `unknown_coin` | Unknown coin |
    | `winner` | Winner |
    | `draw` | Draw |
    | `no_server` | No server |
    | `sleeping` | Sleeping |

  - The module stores the last received texts in flash, so `no_server` can be shown in the right language even without a connection.
  - Server side: translations live in the server's language files, and users can override any text in the settings.
- **Commands:**
  - `state` (full state; idempotent, sent on every change):
    ```json
    {"t":"cmd","id":9,"i":"home","c":"state","d":{"ph":"playing","s":[3,2],"pl":[{"n":"Anna","col":"#E4572E","img":"9f2c41d0"},{"n":"Ben","col":"#29335C","img":null}],"clk":{"left_ms":184000,"run":true},"msg":"golden_goal"}}
    ```
    - `ph` = phase: `idle` / `pregame` / `playing` / `paused` / `finished`
    - `s` = [own score, opponent score], exactly 2 numbers
    - `pl` = this side's players; `img` must be present and may be null
    - `clk` = optional countdown (game clock, or kickoff countdown in `pregame`). The display runs it locally; the server resends it on every change.
    - `msg` = optional **text key** from `txt` (not the text itself), so the display shows it in the configured language.
  - `anim` → `d:{name}`. Unknown names → `nack unknown_command`. Standard animations:

    | Name | When |
    |---|---|
    | `goal_for` | this side scored |
    | `goal_against` | the other side scored |
    | `golden_goal` | time ran out with a tie |
    | `win` / `lose` / `draw` | game over |
    | `shutout_win` / `shutout_lose` | game over with the loser at 0 goals ("destruction"). Sent instead of `win`/`lose` |
    | `kickoff` | a new game starts |
    | `wake` / `sleep` | table wakes up / goes to sleep |

    - **Variants:** a display may have several animations for the same name. It picks one at random, avoiding the one it played last time. The server only ever sends the name. `p.anims` lists names, not variants.
    - If a display doesn't support `shutout_win`/`shutout_lose`, the server sends `win`/`lose` instead.
  - `brightness` → `d:{v:0..100}`
  - `img` → `d:{h, o, data}`: one piece of a picture (`h` hash, `o` byte offset, `data` base64 of up to 600 bytes)
  - `img_end` → `d:{h, crc32}`
- **Events:**
  - `img_missing` → `d:{h}`. Sent when `state` references a picture the module doesn't have. The server then sends the `img` pieces and `img_end`.
  - Pictures are RGB565, `img`×`img` px (64×64 = 8 KB ≈ 14 pieces). The module caches them by hash, removing the least recently used.

### Third-party capabilities
- Named `vendor.name/thing@N`. The manifest ships their event/command/config schemas.
- Without a WASM plugin, the server validates them, shows their events in the module console and on a generic "device" page, but doesn't use them in games.

## 11. Arduino library `KickerModule`
Depends on ArduinoJson 7. Details and examples: `firmware/lib/KickerModule/README.md`.
```cpp
#include <KickerModule.h>

KickerModule km("kicker/goal-sensor", "1.2.0", "rp2040-zero");
GoalSensor goalA("a");
GoalSensor goalB("b");          // a second argument `true` would announce w:1 (wake source)

void setup() {
  Serial.begin(115200);
  km.add(goalA);
  km.add(goalB);
  km.onSleep([] { /* sensors to low power */ });
  km.begin(Serial);
}

void loop() {
  km.loop();                   // handshake, ping/pong, acks, retries, dedup, wrap-around; never blocks
  if (beamABroken()) goalA.goal();
  km.log(KM_INFO, "beam level %d", analogRead(A0));
}
```
- The protocol core (`src/kicker/`) is plain C++ without Arduino dependency and is unit-tested on the PC (`pio test -e native`).
- **Typed helpers** exist for catalog capabilities (`GoalSensor` now; the others with their milestones). Custom capabilities subclass `Capability` and override `onCommand`, `onConfig`, `writeParams`, `onSync`.
- Stateful helpers report their state in `onSync()`, which the library calls after every `ready`.
- **`identify` default:** blinks `LED_BUILTIN`. `reboot` / `update` defaults use the RP2040 / ESP32 functions.
- **Testing without a Pi:** `kicker monitor [port]` (D8) runs the server side of this protocol on a laptop. It shows every line with its validation result, and lets you send commands by hand.

## 12. Server implementation notes
- `internal/protocol`: line reader, strict decoder, catalog, dedup window, schema generation.
- `internal/modules/session`: one conversation with one module – handshake, reboot detection, heartbeat + clock offset, event acks + dedup + payload validation, commands with retries. Used by the module host and by `kicker monitor`.
- The module host (task 6) turns `(uid, instance)` into a role and publishes typed events on the internal event bus, e.g. `GoalDetected{Role: home_goal, At: serverTime}`. The game engine never sees raw JSON.
- Commands go through the same layer: `Display(home).SetState(...)`, `Dispenser().Release(away, 1)`.
- Every message and error is visible live in the module console (SSE) and kept in a rotating log file.

## 13. Open questions
- None.
