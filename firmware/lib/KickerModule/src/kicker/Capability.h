#pragma once
#include <ArduinoJson.h>
#include <stdint.h>

namespace kicker {

class Core;

// nack codes (spec 01 §8)
namespace nack {
constexpr const char* UnknownCommand = "unknown_command";
constexpr const char* BadArgs = "bad_args";
constexpr const char* Busy = "busy";
constexpr const char* HwFault = "hw_fault";
constexpr const char* NotReady = "not_ready";
}  // namespace nack

// Result of a command or config: ok → ack, otherwise nack with a code (and optional message).
struct Result {
  const char* nackCode = nullptr;
  const char* message = nullptr;

  static Result ok() { return Result(); }
  static Result fail(const char* code, const char* msg = nullptr) {
    Result r;
    r.nackCode = code;
    r.message = msg;
    return r;
  }
  bool isOk() const { return nackCode == nullptr; }
};

// One capability instance hosted by this module (spec 01 §10). Subclass it for your own
// capability, or use a typed helper like GoalSensor for the catalog ones.
class Capability {
 public:
  Capability(const char* instance, const char* id, uint8_t version, bool canWake = false)
      : instance_(instance), id_(id), version_(version), canWake_(canWake) {}
  virtual ~Capability() {}

  const char* instance() const { return instance_; }
  const char* id() const { return id_; }
  uint8_t version() const { return version_; }
  bool canWake() const { return canWake_; }

  // Static params sent in announce (`p`). Return false if there are none.
  virtual bool writeParams(JsonObject p) {
    (void)p;
    return false;
  }
  // Partial config from the server: only the keys present must be applied.
  virtual Result onConfig(JsonObjectConst d) {
    (void)d;
    return Result::ok();
  }
  // A command for this instance. Unknown names must return nack::UnknownCommand.
  virtual Result onCommand(const char* name, JsonObjectConst args) {
    (void)name;
    (void)args;
    return Result::fail(nack::UnknownCommand);
  }
  // Called right after every `ready`: stateful capabilities report their current state here.
  virtual void onSync() {}
  // Hardware hooks, called by the library: begin() once from KickerModule::begin(),
  // update() on every loop(). Components like GoalGate read their sensors here.
  virtual void begin() {}
  virtual void update() {}

 protected:
  // Emits an event without data. Returns false if the event queue had to drop an older event.
  bool emit(const char* event);
  // Emits an event without data that happened at module time ts (µs, see Core::toUs64).
  bool emitAt(const char* event, uint64_t ts);
  // Emits an event; fill(JsonObject d) writes the data.
  template <typename Fill>
  bool emit(const char* event, Fill fill);
  // Reports the capability's health (ok / degraded / fault).
  void status(const char* s, const char* msg = nullptr);
  // The protocol core this capability was added to (nullptr before KickerModule::add).
  Core* core() const { return core_; }

 private:
  friend class Core;
  const char* instance_;
  const char* id_;
  uint8_t version_;
  bool canWake_;
  Core* core_ = nullptr;
};

}  // namespace kicker
