// KickerModule – connects an Arduino board to a kicker table server over USB serial.
// Implements the kicker module protocol v1 (spec 01). Example:
//
//   #include <KickerModule.h>
//   KickerModule km("kicker/goal-sensor", "1.0.0", "rp2040-zero");
//   GoalGate goalA("a", 2), goalB("b", 3);   // ready-made components: instance + pins
//
//   void setup() {
//     Serial.begin(115200);
//     km.add(goalA);
//     km.add(goalB);
//     km.begin(Serial);
//   }
//   void loop() {
//     km.loop();                    // never blocks; runs the protocol and all components
//   }
#pragma once

#include "kicker/Core.h"
#include "kicker/GoalSensor.h"

using kicker::Capability;
using kicker::GoalSensor;
using kicker::Result;
namespace nack = kicker::nack;

#define KM_DEBUG kicker::LogDebug
#define KM_INFO kicker::LogInfo
#define KM_WARN kicker::LogWarn
#define KM_ERROR kicker::LogError

#ifdef ARDUINO
#include <Arduino.h>

#include "kicker/GoalGate.h"
using kicker::GoalGate;

class KickerModule {
 public:
  // manifestId: "vendor/name" of the module's manifest. fwVersion: semver. hardware: board name.
  KickerModule(const char* manifestId, const char* fwVersion, const char* hardware);

  // Registers a capability instance. Call before begin(). Returns false if the instance name
  // is used twice or more than 16 instances are added.
  bool add(Capability& cap) { return core_.add(cap); }

  // Starts the protocol on a stream, usually Serial (USB). Sends the first announce.
  void begin(Stream& stream);

  // Handles the protocol. Never blocks; call it on every loop() iteration.
  void loop();

  // Overrides the unique id. By default it's read from the chip (RP2040 flash id, ESP32 MAC).
  void setUid(const char* uid) { core_.setUid(uid); }

  template <typename... Args>
  void log(kicker::LogLevel level, const char* fmt, Args... args) {
    core_.log(level, fmt, args...);
  }

  // Callbacks. Defaults: identify blinks LED_BUILTIN; reboot and update use the board's
  // reboot / bootloader functions where available.
  void onReady(kicker::Core::BoolFn fn) { core_.onReady(fn); }
  void onOrphaned(kicker::Core::BoolFn fn) { core_.onOrphaned(fn); }
  void onSleep(kicker::Core::VoidFn fn) { core_.onSleep(fn); }
  void onWake(kicker::Core::VoidFn fn) { core_.onWake(fn); }
  void onIdentify(kicker::Core::IdentifyFn fn) { core_.onIdentify(fn); }
  void onReboot(kicker::Core::VoidFn fn) { core_.onReboot(fn); }
  void onUpdate(kicker::Core::VoidFn fn) { core_.onUpdate(fn); }

  bool ready() const { return core_.ready(); }
  bool adopted() const { return core_.adopted(); }
  bool orphaned() const { return core_.orphaned(); }
  kicker::Core& core() { return core_; }

 private:
  class StreamIo : public kicker::Io {
   public:
    Stream* stream = nullptr;
    int read() override { return stream ? stream->read() : -1; }
    void write(const char* data, size_t len) override {
      if (stream) stream->write(reinterpret_cast<const uint8_t*>(data), len);
    }
    void flush() override {
      if (stream) stream->flush();
    }
    uint32_t micros() override { return ::micros(); }
  };

  static void defaultIdentify(uint16_t seconds);
  void updateIdentifyLed();

  char fwName_[48];
  StreamIo io_;
  kicker::Core core_;
  uint32_t blinkUntilMs_ = 0;
  bool blinking_ = false;
  static KickerModule* instance_;
};

#endif  // ARDUINO
