// KickerModule – Arduino library for kicker modules (spec 01).
//
// Skeleton only. The protocol core (hello/announce, ping/pong, acknowledged events,
// commands, config, logging) is milestone M1, task 3.
#pragma once

#include <Arduino.h>

#define KICKER_PROTOCOL_VERSION 1
#define KICKER_MODULE_LIB_VERSION "0.1.0"

class KickerModule {
 public:
  KickerModule(const char* manifestId, const char* firmwareVersion, const char* hardware)
      : manifestId_(manifestId), firmwareVersion_(firmwareVersion), hardware_(hardware) {}

  // Call once in setup(), after Serial.begin().
  void begin(Stream& io) { io_ = &io; }

  // Call on every loop() iteration. Handles the protocol; never blocks.
  void loop() {}

  const char* manifestId() const { return manifestId_; }
  const char* firmwareVersion() const { return firmwareVersion_; }
  const char* hardware() const { return hardware_; }

 private:
  const char* manifestId_;
  const char* firmwareVersion_;
  const char* hardware_;
  Stream* io_ = nullptr;
};
