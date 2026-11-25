#pragma once
// GoalGate: a ready-made goal_sensor@1 for one IR break-beam light gate on one pin.
// Arduino only (uses pinMode / digitalRead / attachInterrupt).
#ifdef ARDUINO
#include <Arduino.h>

#include "BeamDetector.h"
#include "GoalSensor.h"

namespace kicker {

// Usage:  GoalGate goalA("a", 2);  km.add(goalA);   – that's all, the library does the rest.
//
// Wiring for open-collector receivers (e.g. Adafruit 2168): signal to `pin`, the internal pull-up
// is enabled. Never pull the signal up to 5 V on a 3.3 V board.
class GoalGate : public GoalSensor {
 public:
  static const uint8_t kMaxGates = 8;  // gates with their own interrupt; more fall back to polling

  // brokenLevel: pin level while the beam is broken (Adafruit 2168: LOW).
  // canWake: announce the gate as a wake source. Off by default: the server ignores goal sensors
  // while the table sleeps, because the gates are switched off then (spec 02 §9).
  GoalGate(const char* instance, uint8_t pin, uint8_t brokenLevel = LOW, bool canWake = false)
      : GoalSensor(instance, canWake), pin_(pin), brokenLevel_(brokenLevel) {}

  uint8_t pin() const { return pin_; }
  bool blockedNow() const { return digitalRead(pin_) == brokenLevel_; }
  const BeamDetector& detector() const { return detector_; }

  // --- called by the library ---
  void begin() override;
  void update() override;
  void onSync() override;
  Result onConfig(JsonObjectConst d) override;

  void onEdge();  // interrupt handler (public for the trampoline)

 private:
  void applyConfig();
  void report(BeamDetector::Event ev);
  void trace(uint64_t now);

  // Every interruption is logged at debug level ("gate a: blocked 11.84 ms -> counted"), at most
  // kTracePerSec per second, so the module console doubles as the bench rig's measuring tool.
  static const uint8_t kTracePerSec = 5;
  uint32_t traceSec_ = 0;
  uint8_t traceCount_ = 0;
  uint16_t suppressed_ = 0;

  uint8_t pin_;
  uint8_t brokenLevel_;
  BeamDetector detector_;
  int8_t slot_ = -1;  // interrupt slot, -1 = polling
  bool lastPolled_ = false;

  static const uint8_t kRing = 16;
  volatile uint32_t times_[kRing] = {};
  volatile uint8_t blocked_[kRing] = {};
  volatile uint8_t head_ = 0;
  uint8_t tail_ = 0;
};

}  // namespace kicker
#endif  // ARDUINO
