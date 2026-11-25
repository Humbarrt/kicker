# KickerModule

Arduino library that connects a board to a kicker table server over USB serial
(kicker module protocol v1, spec 01). It handles the handshake, heartbeat, clock,
acknowledged events with retries, command deduplication, config and logging, so a module
only has to read its sensors and call `goal()`, `emit()` and friends.

Works on any board with an Arduino core and USB serial. Reference boards: RP2040-Zero, RP2350.
Depends on ArduinoJson 7.

## Components: a module is a list of parts with their pins
```cpp
#include <KickerModule.h>

KickerModule km("kicker/goal-sensor", "1.0.0", "rp2040-zero");
GoalGate goalA("a", 2);         // instance name (mapped to a role in the server UI) + pin
GoalGate goalB("b", 3);

void setup() {
  Serial.begin(115200);
  km.add(goalA);
  km.add(goalB);
  km.begin(Serial);
}

void loop() {
  km.loop();                    // never blocks: runs the protocol and updates every component
}
```
Wiring is decided in the firmware, behaviour (timings, thresholds) comes from the server as config.
Combining capabilities on one board means adding more components – pin clashes are visible in one
place. Ready-made components: `GoalGate` (more with their milestones).

If you read the sensor yourself, use the plain capability instead, e.g. `GoalSensor goalA("a");`
and call `goalA.goal()`.

## Light gates: BeamDetector
`GoalGate` uses `kicker/BeamDetector.h`, which turns raw light-gate edges into goals: blocks shorter than `min_block_us`
are noise, a goal is reported as soon as the beam has been blocked long enough (timestamped at the
start of the block), bounces within `lockout_ms` are ignored, and a beam blocked for 10 s becomes a
fault. It is hardware-free; use it directly for other kinds of gates: feed it interrupt-timestamped edges
(`km.core().toUs64(micros32)`), poll it, and report with `sensor.goal(detector.goalTime())`.

## Your own capability
Subclass `Capability`; the id must be namespaced (`vendor.name/thing`). Override `begin()` /
`update()` to read hardware – the library calls them for you.
```cpp
class Horn : public Capability {
 public:
  Horn() : Capability("horn", "acme.party/horn", 1) {}
  Result onCommand(const char* name, JsonObjectConst args) override {
    if (strcmp(name, "honk") != 0) return Result::fail(nack::UnknownCommand);
    int ms = args["ms"] | 300;
    startHonk(ms);
    return Result::ok();
  }
  void pressed() { emit("pressed", [](JsonObject d) { d["strength"] = 3; }); }
};
```

## What the library guarantees
- Events are kept until the server acknowledges them and resent every 200 ms (5 times),
  paused while the server is gone, and resent with a new session. Up to 8 are queued.
- A repeated command (same id) is answered again but not executed twice.
- Lines that aren't JSON are ignored, so `Serial.println()` debugging doesn't break anything –
  the server shows them in its module console. Prefer `km.log(KM_INFO, "x=%d", x)`.
- Default `identify` blinks `LED_BUILTIN`; `reboot` / `update` use the RP2040 / ESP32 functions.

## Tests
The protocol core (`src/kicker/`) has no Arduino dependency and is tested on the PC:
```sh
pio test -e native
```
