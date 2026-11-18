// Goal sensor module (spec 01, goal_sensor@1): instance "a" and "b", one IR break-beam each.
// Skeleton only – the real firmware is milestone M1, task 5.
#include <Arduino.h>
#include <KickerModule.h>

KickerModule km("kicker/goal-sensor", "0.1.0", "rp2040-zero");

void setup() {
  Serial.begin(115200);
  km.begin(Serial);
}

void loop() {
  km.loop();
}
