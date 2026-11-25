# Spec 02: Game engine

Status: **Approved v4** (2026-11-23: no queued games – paused games are resumed by hand or by smart wake, §8, §9). Builds on D2, D3, D9, D10, D16, D17 in `00-architecture-decisions.md` and on the capability catalog in `01-module-protocol.md`.

## 1. Scope
The game engine is the part of the server that turns module events and user actions into games:
- who is playing (registrations)
- when a game starts, where the ball goes, when it ends
- pause, sleep, wake
- what the displays show
- which events are written to the database

It doesn't cover ratings (D9), tournaments (D11) or post-game editing permissions (spec 03 §4), except where they hook in.

**Permissions:** every change made through the web or app (score, players, pause, start, win conditions) requires a login whose group has the matching permission (spec 03 §4). The Anybody group grants none of them by default, so without login the site is read-only.

## 2. Architecture
- **One goroutine owns all table state** (an actor). Everything that can change the table goes through its input channel and is processed strictly in order:
  - module events (already typed by the module host, spec 01 §12)
  - API commands (web/app)
  - timer ticks
- The core is a pure function:
  `Step(state, input, now) → (newState, []DomainEvent, []Effect)`
  - `DomainEvent` = rows to append to `game_events` (and `table_registrations`, see §4).
  - `Effect` = module commands (display state, dispenser release, coin eject, sleep/wake) and SSE messages.
- **Order:** persist the domain events first, then run the effects. A SQLite write on an SSD takes about 1 ms, so the dispenser still reacts within a few ms of a goal.
- **Restart recovery:**
  - the current game is rebuilt from its events
  - registrations from `table_registrations`, corrected by the modules' state sync (spec 01 §6)
  - `balls_out` from the settings row (§6.1)
- **Simulation:** because `Step` is pure, the same code runs in a simulator with fake modules. It powers the unit tests, the integration tests and the load test (100k games generated through the real engine).

## 3. Table states
| State | Meaning |
|---|---|
| `sleeping` | modules asleep, displays off |
| `idle` | awake, no game, kickoff requirement not met |
| `pregame` | requirement met: kickoff countdown running, or waiting for the ball to come back (§6.1) |
| `playing` | a game is running (it may be paused by hand, §7.4) |
| `finished` | results shown for `finished_hold_s`, then the next game or `idle` |

Transitions:
- `sleeping` → `idle` on wake
- `idle` ↔ `pregame` as the kickoff requirement is met / no longer met
- `pregame` → `playing` at kickoff
- `playing` → `finished` when the game is won or drawn
- `playing` → `sleeping` when the game pauses after inactivity (the game becomes `paused`, D16)
- `finished` → `pregame` / `idle` after the hold time
- `idle` / `pregame` / `finished` → `sleeping` after inactivity

**Goals only count while a game is `playing`.** In any other state, a goal sensor trigger only means "a ball went back into the dispenser" (§6.1).

## 4. Registrations (who is playing)
Each side has **2 slots**. A **registration** = a player (or an unknown coin) in a slot, with one or more **sources**:

| Source | Becomes active | Becomes stale | Removed |
|---|---|---|---|
| `coin` (uid) | `inserted` / `held` | `ejected` by the button | `ejected` by the server (game end, pause, sleep) |
| `corner` (corner id) | API claim (see §5) | `absent` | at the next kickoff while stale |
| `manual` (UI) | added in the UI | – | at game end, or removed in the UI |

- A registration is **active** while at least one of its sources is active, and **stale** when all of its sources are stale.
- Registrations are stored in `table_registrations` (side, slot, player_id NULL, coin_uid NULL) with their sources in `table_registration_sources`, so they survive restarts and updates. The status is derived from the sources.

### Rules for a new registration on side S
1. **Same player already on S:** nothing changes, the new source is added to the existing registration.
2. **Same player on the other side:** the player moves to S.
3. **S has a free slot:** the player takes the first free slot.
4. **Every registration on S is stale:** **the whole side is replaced.** Both old players are removed and the new player takes slot 1. This also applies right after a new game has started.
5. **One registration on S is stale:** it is replaced by the new player.
6. **Both registrations on S are active:** rejected. The app shows "side full". A third coin can't physically fit anyway.

The guest player is exempt from rules 1 and 2: several guests can play at once.

### During a game
- While a game is in `pregame` or `playing`, **the registrations are the game's roster.** Every change (join, replacement, removal) is written as a game event (`player_joined` / `player_left`, source `system`) and can be corrected later like any other edit (D2).
- Stale players **stay in the current game**. They keep counting for it unless rules 4 or 5 replace them.
- At the **next kickoff**, stale registrations are removed ("removed from the next game").
- A **resumed** game keeps its own roster; registration changes during it are for the game after it (§9).

### Unknown coins
- An unknown coin creates a registration with `coin_uid` and no player. The displays show `unknown_coin`, and the web/app prompt "assign this coin" (members).
- Assigning it while the coin is still in the game fills in the player (`player_assigned` event) and saves the coin mapping.
- If nobody assigns it, the slot counts as **guest** for that game. Assigning it after the game is an edit.
- Coin mappings are resolved when the registration is made. Moving a coin to another player later never changes past games.

## 5. Corner claim flow (phones)
1. The phone reads the corner's NFC tag: `https://112233444.xyz/c?s=<server_id>&c=home_1` (spec 03 §11, spec 08); the app opens through the link, or the browser forwards to the kicker's `/claim` page.
2. The app picks the account stored for that `server_id` and calls `POST /api/table/claims {corner}`. The player is the account's linked player, so **using a phone requires a member account**. Players without an account use coins.
3. The claim registers the player on that corner's side (rules in §4), source `corner`, and **wakes the table**.
4. If `present` arrives at that corner within `corner_claim_timeout_s` (30 s), the phone is tracked: `absent` later makes the registration stale.
5. If the phone is never put down (or the corner has no presence sensor), the claim counts **for one game only**: it is removed at game end, like a manual registration.
6. A new claim on a corner that already has a claim takes it over. The previous player's corner source becomes stale.

## 6. Kickoff
- **Requirement** (`kickoff_requires`):
  - `both_sides` (default): at least one registration on each side
  - `any_player`: at least one registration on either side
  - Stale registrations don't count.
  - A member can always start a game by hand with "Start now", even with empty slots.
- When the requirement is met, the table enters `pregame` and starts a `kickoff_delay_s` countdown (default 5 s), shown on the displays.
  - **Every roster change restarts the countdown**, so a group can finish registering.
  - "Start now" in the UI skips the countdown.
- **Kickoff only happens once the ball is back in the dispenser** (§6.1). If it isn't, the countdown waits and the displays ask for the ball.
- At kickoff:
  1. The game is created. The rules are copied from the current preset (or the member's adjusted copy, §7.3), or from the tournament stage. Event `game_started` (side = first ball), then `player_joined` for each slot.
  2. The displays play `kickoff`.
  3. The dispenser releases the first ball to the `first_ball` side.
- **First ball** (`first_ball`):
  - `random` (default)
  - `lower_rating`: the side with the lower average rating
  - `loser_previous`: the side that lost the last game
  - `alternate`
  - `home` / `away`
  - A tournament can set the side per game, overriding this setting.
- **Without a dispenser:** the game starts when the countdown ends (or via "Start now"), and the game clock starts at kickoff.

### 6.1 Ball tracking: "give me my ball back"
The dispenser doesn't count its balls, but the server knows how many balls are **on the field**:
- `balls_out` += count on every `released` event
- `balls_out` −= 1 on every goal sensor trigger, whether it counts as a goal or not, because the ball rolls back into the dispenser. It never goes below 0.

Normally the last goal returns the ball, so `balls_out` is 0 when a game ends. It stays at 1 when the game ended **without** a final goal: time limit, draw, or a game paused after inactivity.

- **Before a kickoff with `balls_out > 0`:** the table waits. The displays show `msg return_ball` ("Put the ball in a goal"). The next goal sensor trigger only returns the ball; it doesn't score. Then the kickoff continues normally, and the dispenser releases the first ball to the correct side.
- **Resuming a paused game:**
  - With `balls_out > 0`, the ball is still on the field, so nothing is released and play simply continues.
  - With `balls_out = 0`, the dispenser releases a ball to `next_ball_side`.
- **Correcting the count:** after `release_all` or when balls are taken off the table by hand, the count can be wrong. A member can reset it in the UI ("all balls are in the dispenser" / "a ball is on the field").
- **Without a dispenser:** no tracking, nothing to wait for.

## 7. During a game
### 7.1 Goals
On `goal` from `home_goal` (ball in the home net) → a point for **away**, and the other way round:
1. Append `goal` (scoring side, source `sensor`, `play_time_ms`, server time from the module clock offset). `balls_out` −= 1.
2. Check the win condition (D10: target = min(max(win_score, lower_score + win_by), max_score)).
3. Not won: the dispenser releases the next ball to the side given by `after_goal_ball` (`conceding` (default) | `scoring` | `alternate`). Then display `state` updates, `goal_for` on the scoring side and `goal_against` on the other.
4. Won: see §8. No ball is released.

### 7.2 Time limit
- The game clock counts **play time**: it starts at kickoff and excludes pauses. Displays get `clk` and count down by themselves.
- When it runs out (`clock_expired`):
  - one side leads → it wins (the ball is still on the field → §6.1)
  - tied + `golden_goal` → `golden_goal` event, displays play `golden_goal` and show `msg golden_goal`. The next goal wins.
  - tied + `draw` → draw (free play only; tournaments always use golden goal)

### 7.3 Live corrections (scoreboard page, members only)
- +1 / −1 per side
- undo the last goal
- swap sides
- add / remove a player
- reset the ball count (§6.1)
- **change the win conditions** of the current game, or of the next game before kickoff (`win_score`, `win_by`, `max_score`, `time_limit_s`, `time_tie`; no draws in tournaments). The game has its own copy, so the preset stays unchanged. Logged as `rules_changed`; the win check runs again immediately.

Each correction is a normal event with source `table` and the member as author. −1 and undo also work while the game is paused by hand; +1 resumes it like a goal.

### 7.4 Pause and "Start new game" (members only)
The UI never abandons a game. Games only become `abandoned` after being paused for `abandon_after_hours` (D16).
- **Pause:** the game clock stops and the displays show `paused`. The game is resumed by:
  - **the next goal**: it counts, and the game continues from there
  - or **"Resume"** in the UI
  - For games with a time limit, the clock restarts at whichever comes first, so "Resume" gives the more accurate clock.
- **Start new game:** the running game is paused (or discarded if it has no goals, D16) and goes to the paused games list. The table goes to `pregame` with the current registrations (or `idle` if the kickoff requirement isn't met).
- **Resume** (from the paused games list): see §9.

## 8. Game end
1. Append `finished` (winner side or NULL for a draw; reason `score` / `time` / `golden_goal`).
2. Displays: `win`/`lose`/`draw`. If the losing side has 0 goals, `shutout_win`/`shutout_lose` instead.
3. Both coin acceptors eject (`eject` command). Coin registrations are removed. Manual registrations and one-game claims are removed. Corner registrations with a tracked phone stay. Exception: after a **resumed** game the registrations are kept for the game after it (§9).
4. The rating projection is updated, and `stats_changed` is published over SSE.
5. After `finished_hold_s` (default 10 s):
   - if the kickoff requirement is met (e.g. phones still on the corners) → `pregame` (waiting for the ball if needed, §6.1)
   - else → `idle`
   - Paused games never continue by themselves; they wait in the paused games list.

## 9. Pause after inactivity, sleep, wake
**Activity** = any module event, any registration change, any table action in the UI.

- **Playing, no goal and no activity for `pause_after_minutes`** (default 5):
  - The game is paused (`paused`, reason `timeout`). **The pause is backdated to the last activity**, so the idle minutes don't count as play time.
  - The coins are ejected. The players remain the paused game's participants, and their coin registrations are removed.
  - The table goes to sleep. The ball is usually still on the field (§6.1).
- **Paused by hand, no activity for `sleep_after_minutes`:** the game leaves the table into the paused games list and the table goes to sleep.
- **Idle / pregame / finished, no activity for `sleep_after_minutes`** (default 3):
  - The table goes to sleep.
  - Coins are ejected and their registrations removed.
  - Corner registrations are only kept if the phone is lying there (`present`).
- **Sleep:** displays play `sleep`, then every module gets `sleep`. Modules that come online while the table sleeps get `sleep` too.
- **Wake sources:**
  - coin `inserted`
  - corner claim (API)
  - any table action in the UI, including a "Wake up" button
  - The coin acceptors' state sync and eject reports don't wake the table, and neither do the goal sensors: they are off while the table sleeps (spec 01 `goal_sensor@1`). A trigger that still arrives only returns the ball.
  - On wake, every module gets `wake` and the displays play `wake`.
- **Paused game on wake** (`on_wake`). It only applies to the most recently paused game, and only **as long as no new game was started since it was paused**:
  - `smart` (default): after a wake, resume it as soon as **at least one of its players is registered again for each side** it had players on (coin, corner claim or added by hand; the guest and unknown coins don't count). Until then the normal kickoff flow goes on; if a new game starts first, the paused game stays in the paused list. "Start new game" without a sleep never triggers it.
  - `resume`: always resume it.
  - `new_game`: never; always start fresh.
  - Once a new game has started, the paused game is only resumed by hand from the paused games list.
- **Resuming** (on wake, or by hand from the paused games list, D16):
  - `resumed` event. The game keeps its own participants, while current registrations wait for the game after.
  - A game running at that moment is discarded if it has no goals, otherwise paused (reason `switched`) into the paused games list.
  - The displays show the score again.
  - Ball: see §6.1.

## 10. What the displays show
| Table / game state | `ph` | Content |
|---|---|---|
| idle | `idle` | registered players of this side, `msg waiting` |
| pregame | `pregame` | players, `clk` = kickoff countdown, `msg kickoff_in` |
| pregame, ball not back | `pregame` | players, `msg return_ball` |
| playing | `playing` | score, players, `clk` if there is a time limit; `msg golden_goal` during golden goal |
| paused (by hand, or briefly before sleeping) | `paused` | score, players, `msg paused` |
| finished | `finished` | final score, `msg winner` on the winning side / `msg draw` |
| sleeping | – | display asleep |
| unknown coin on this side | any | `msg unknown_coin` until it's assigned |

## 11. Event model
`game_events` uses columns only, no JSON:

| Column | Notes |
|---|---|
| `id`, `game_id`, `seq` | `seq` counts up per game |
| `type` | see below |
| `side` NULL | `home`/`away` |
| `slot` NULL | 1/2 |
| `player_id` NULL, `coin_uid` NULL | who |
| `via` NULL | `coin` / `corner` / `manual` (for `player_joined`) |
| `target_seq` NULL | for corrections (e.g. which goal is voided) |
| `value` NULL | integer, e.g. ball count |
| `rule` NULL, `rule_value` NULL | for `rules_changed`: which rule column and its new value |
| `source` | `sensor` / `table` / `manual` / `system` |
| `author_account_id` NULL, `reason` NULL | for live corrections and edits |
| `created_at` | server time (module time corrected for sensor events) |
| `play_time_ms` NULL | for goals and clock events |

Event types:
- `game_started` (side = first ball), `finished`, `paused` (reason `manual` / `timeout` / `new_game` / `switched`), `resumed`, `abandoned`, `reinstated`, `game_voided`
- `player_joined`, `player_left`, `player_assigned`
- `rules_changed` (one event per changed rule)
- `ball_released`
- `goal` (`side` = scoring side), `goal_voided`
- `sides_swapped`
- `clock_expired`, `golden_goal`
- `reverted` (`target_seq` = a correction; takes it back – reverting a `reverted` brings the correction back)

**Corrections of finished games** (D2, source `manual`, with the author; no reason field): one event each – `goal` (a goal added), `goal_voided`, `player_joined` (who sat in a slot; replaces whoever sat there), `player_left`, `sides_swapped` (players and score swap together), `game_voided`. After corrections the winner is the side with more goals (a tie is a draw). The game's projections (result, participants, `voided`, `edits`) and the ratings from that game on are recalculated.

Discarded games (D16: no goals) are deleted entirely and leave no events. Ball returns outside a game are not game events; they only change `balls_out`.

## 12. Running with fewer modules
The engine checks which roles are adopted and online, and degrades gracefully:

| Missing | Behaviour |
|---|---|
| goal sensors | score with +1/−1 on the scoreboard page (score keeper mode, members) |
| dispenser | kickoff after the countdown or via "Start now"; no ball-side logic, no ball tracking |
| coin acceptors + corners | players are added in the UI (manual registrations) |
| displays | web scoreboard page only |

If a module goes offline during a game, the scoreboard page shows a warning and manual scoring stays available.

## 13. Settings (defaults)
| Setting | Default |
|---|---|
| `kickoff_requires` | `both_sides` |
| `kickoff_delay_s` | 5 |
| `first_ball` | `random` |
| `after_goal_ball` | `conceding` |
| `finished_hold_s` | 10 |
| `pause_after_minutes` | 5 |
| `sleep_after_minutes` | 3 |
| `on_wake` | `smart` |
| `abandon_after_hours` | 24 |
| `corner_claim_timeout_s` | 30 |

## 14. Decided in review (2026-11-18)
- All changes via web/app require a login with the matching permission (spec 03 §4).
- Coins are ejected when a game pauses; reinserting resumes it via `smart`.
- On sleep, only phones lying on a corner stay registered.
- Goals only count while a game is playing. Outside a game, a goal sensor trigger returns the ball.
- After a game without a final goal, the table asks for its ball back before the next kickoff.
- The UI pauses or starts a new game, but never abandons.
- A manual pause ends with the next goal (which counts) or "Resume".
- Members may change the current/next game's win conditions (`rules_changed`), but not presets.
- `kickoff_requires` defaults to `both_sides`. 5 min until pause, 3 min until sleep.
- (2026-11-23) **No queued games.** A game interrupted by resuming another one goes to the paused games list and is only resumed by hand. Smart wake only brings back the most recently paused game while no new game has been started since, and only once one of its players per side is back.

## 15. Open questions
- None.
