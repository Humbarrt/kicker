#pragma once
#include "Capability.h"

namespace kicker {

// Typed helper for goal_sensor@1 (spec 01 §10): one instance per goal.
// The sensor logic itself (beam reading, noise filter, lockout) lives in the module firmware.
class GoalSensor : public Capability {
 public:
  struct Config {
    uint32_t lockoutMs = 1000;  // ignore new triggers after a goal
    uint32_t minBlockUs = 300;  // shorter interruptions are noise
  };
  typedef void (*ConfigFn)(const Config&);

  explicit GoalSensor(const char* instance, bool canWake = false)
      : Capability(instance, "goal_sensor", 1, canWake) {}

  // Reports a goal now. Returns false if the event queue had to drop an older event.
  bool goal() { return emit("goal"); }
  // Reports a goal that happened at module time atUs (the moment the beam was broken).
  bool goal(uint64_t atUs) { return emitAt("goal", atUs); }
  void fault(const char* msg) { status("fault", msg); }
  void ok() { status("ok"); }

  const Config& config() const { return config_; }
  void onConfigChanged(ConfigFn fn) { onConfigChanged_ = fn; }

  Result onConfig(JsonObjectConst d) override {
    Config next = config_;  // partial update: only keys present change (spec 01 §4)
    if (!readUint(d, "lockout_ms", next.lockoutMs) || !readUint(d, "min_block_us", next.minBlockUs)) {
      return Result::fail(nack::BadArgs, "lockout_ms and min_block_us must be non-negative integers");
    }
    config_ = next;
    if (onConfigChanged_) onConfigChanged_(config_);
    return Result::ok();
  }

 private:
  static bool readUint(JsonObjectConst d, const char* key, uint32_t& out) {
    JsonVariantConst v = d[key];
    if (v.isNull()) return true;
    if (!v.is<uint32_t>()) return false;
    out = v.as<uint32_t>();
    return true;
  }

  Config config_;
  ConfigFn onConfigChanged_ = nullptr;
};

}  // namespace kicker
