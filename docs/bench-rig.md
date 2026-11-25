# Spec 07: Goal sensor bench rig + M1 acceptance

Status: **Draft v1** (2026-11-20). M1 task 12 (spec 04 §7, §9). The bench rig proves the goal sensor with real balls before anything is built into the terrazzo; the second half is the checklist that closes M1.

## 1. What is tested
The goal sensor module (RP2040-Zero or Pico W, firmware `kicker/goal-sensor` ≥ 0.4.0) with **two IR break-beam gates** (Adafruit 2168, 5 mm LEDs, open-collector receiver, response < 2 ms), one per goal, each across a section of the tube below the goal.

The questions the rig answers:
1. Is **every** ball counted – dropped straight down and rolled slowly or fast through a sloped tube?
2. Is **nothing else** counted – bounces, vibration, a hand near the tube, sunlight or a phone torch?
3. How long does a ball block the beam (fastest / slowest case)? The noise filter (`min_block_us`, default 300 µs) must stay far below the shortest real block.
4. Is the bounce lockout (`lockout_ms`, default 1000 ms) long enough for the real tube, and short enough for two quick goals?

## 2. The rig (first version: no CAD needed)
- **Tube:** any opaque tube with an inner diameter a few mm larger than the ball (e.g. a 40–50 mm pipe for 35 mm balls), about 30 cm long. The final tube is not decided yet (spec 05 "ball return route").
- **Gate:** two holes across the tube's diameter, 5 mm, opposite each other. Emitter on one side, receiver on the other, both housings against the tube (tape, hot glue or a clamp), LEDs looking through the holes.
  - **Beam height:** for the sloped (rolling) test the beam must cross the ball's centre, i.e. about one ball radius above the tube floor. A beam through the tube's centre also works as long as the tube is less than ~1.5 ball diameters wide.
  - Opaque tube or holes: clear plastic lets IR through, but sunlight too.
- **Two gates** (gate a and gate b), either on two tube pieces or 10 cm apart on one – then a ball must count on both.
- **Positions:** vertical (drop test) and sloped 5–15° (roll test). A stack of books is enough.
- **Wiring** (as in `firmware/modules/goal-sensor/src/main.cpp`):

| Wire | RP2040-Zero / Pico W |
|---|---|
| receiver white (signal) gate a | GP2 |
| receiver white (signal) gate b | GP3 |
| receiver red | 3V3 – **never 5 V** on the signal side |
| emitter red | 5 V (VBUS) for more range |
| all black | GND |

A printed gate holder (clamp ring for the tube with sockets for emitter and receiver, beam height as a parameter) follows once the tube is chosen (§6).

## 3. Measuring tools (already in the software)
- **Module console** (Admin → Modules → Console, or `kicker monitor`): firmware 0.4.0 logs every interruption of a beam, at most 5 lines per second per gate:
  ```
  gate a: blocked 11.84 ms -> counted
  gate a: blocked 0.21 ms -> noise
  gate a: blocked 9.02 ms -> lockout
  gate b: blocked 10412.00 ms -> stuck
  ```
  After every connect: `gate a (pin 2): clear, broken level LOW` – the quickest wiring check.
- **Settings** per gate (Admin → Modules → Settings): lockout (ms) and noise filter (µs). They are sent to the module at once and stored on the server.
- **Scoreboard**: start a game with custom rules (e.g. first to 99) and count.
- **Simulator** is not involved: run `make dev` without the virtual module, or ignore it.

## 4. Test protocol
Record the results in a table (test, gate, tries, counted, wrong, shortest/longest block).

| # | Test | How | Pass |
|---|---|---|---|
| T1 | Wiring | Connect the module, open the console | Both gates report `clear`; covering a beam with a finger logs a block |
| T2 | Drop | Tube vertical, drop the ball from the goal's height, 50× per gate | 50/50 counted |
| T3 | Slow roll | Tube at ~5°, let the ball roll from a stand-still, 30× per gate | 30/30 counted; note the **longest** block |
| T4 | Fast throw | Throw / shoot the ball through, 30× per gate | 30/30 counted; note the **shortest** block. Must be ≥ 10 × `min_block_us` |
| T5 | Bounce | Drop the ball so it bounces back through the gate (or push it back by hand within 1 s) | Counted once; the bounce shows as `lockout` |
| T6 | Quick double | Two balls 1.5 s apart | Both counted |
| T7 | Stuck ball | Hold a ball in the beam for 15 s | No extra goal; after 10 s the module page shows **fault**; it recovers when the ball is removed |
| T8 | Light | 10 min idle near a window, phone torch shone into the tube and at the receiver, room light switched on/off | 0 goals; at most occasional `noise` lines |
| T9 | Vibration | Knock on the tube and the bench, wiggle the cables | 0 goals |
| T10 | Replug | Unplug USB mid-game, drop 2 balls while unplugged (they can't count), replug, drop 1 | The module reconnects within 3 s; the 1 ball after replug counts; nothing is counted twice |
| T11 | Latency | Film the tube and the scoreboard with a phone (slow motion, 240 fps) | Beam broken → scoreboard updated in < 100 ms |

If T4's shortest block is close to the noise filter, lower `min_block_us`; if T5 bounces arrive later than 1 s, raise `lockout_ms`. Write the final values into this spec – they become the defaults for the table.

## 5. M1 acceptance (spec 04 §7 "done when")
| # | Criterion | How to check | Done |
|---|---|---|---|
| 1 | Module appears within 3 s, can be adopted, instances get `home_goal` / `away_goal` | Plug in, Admin → Modules, adopt with roles | |
| 2 | Log in, "Start game", breaking a beam updates the web scoreboard in < 100 ms | T11 | |
| 3 | The game ends at the preset's target score, and the game and its events are stored | Play an Official game (5, win by 2, max 8) to the end; restart the server; it shows as the last game with all goals | |
| 4 | Server restart mid-game: the game continues | Ctrl-C during a game, start again, keep scoring | |
| 5 | Unplug/replug mid-game loses no goals | T10. (The module is powered by USB, so a ball dropped while unplugged can't count – that is expected. What must not happen: a goal that the module sent before the unplug is lost, or one is counted twice.) | |
| 6 | The same flow works with `--sim` and no hardware | `make dev`, simulator buttons | ✓ (verified 2026-11-20) |

When all six are ticked, M1 is done.

## 6. Open
- Ball diameter of the balls in use, and the tube for the real ball return (spec 05 "Ball return route") – the printed gate holder is designed for that tube.
- Printed gate holder: parametric (tube diameter, beam height, sensor housing – measure it; Adafruit lists 20 × 10 × 8 mm for the 3 mm version), made where the other mechanical parts are made.
- Final `lockout_ms` / `min_block_us` defaults from T4/T5.
