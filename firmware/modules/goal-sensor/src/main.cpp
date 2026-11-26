// Goal sensor module (spec 01, goal_sensor@1): two IR break-beam light gates, one in the tube
// below each goal. The server maps instance "a" / "b" to home_goal / away_goal on adoption.
//
// Wiring (Adafruit 2168 or similar open-collector receivers):
//   receiver white (signal) → pin below, internal pull-up – NEVER pull up to 5 V
//   receiver red → 3V3, black → GND;  emitter red → 5V (more range), black → GND
//
// Combining capabilities on one board = adding more components to this list.
#include <Arduino.h>
#include <KickerModule.h>

#ifndef BOARD_NAME  // set by the Arduino core, e.g. "rpipicow"
#define BOARD_NAME "unknown"
#endif

// KM_MANIFEST and KM_VERSION come from platformio.ini ([kicker]).
KickerModule km(KM_MANIFEST, KM_VERSION, BOARD_NAME);

//             instance  pin  level while broken (Adafruit 2168: LOW)
GoalGate goalA("a",      2,   LOW);
GoalGate goalB("b",      3,   LOW);

void setup() {
  Serial.begin(115200);
  km.add(goalA);
  km.add(goalB);
  km.begin(Serial);
}

void loop() {
  km.loop();
}
