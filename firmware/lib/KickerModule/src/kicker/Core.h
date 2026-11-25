#pragma once
// Protocol core of the KickerModule library (spec 01). Plain C++, no Arduino dependency,
// so it can be unit-tested on a PC. KickerModule.h connects it to an Arduino Stream.
#include <ArduinoJson.h>
#include <stddef.h>
#include <stdint.h>

#include "Capability.h"
#include "Clock64.h"
#include "LineBuffer.h"

#ifndef KM_MAX_LINE
#define KM_MAX_LINE 1024  // spec 01 §2, including the newline
#endif
#ifndef KM_EVENT_QUEUE
#define KM_EVENT_QUEUE 8  // spec 01 §7
#endif
#ifndef KM_MAX_EVENT_LEN
#define KM_MAX_EVENT_LEN 256  // longest event line kept for retransmission
#endif
#ifndef KM_MAX_CAPS
#define KM_MAX_CAPS 16  // spec 01 §5
#endif

namespace kicker {

constexpr int kProtocolVersion = 1;
constexpr uint64_t kEventRetryUs = 200000;     // resend an unacknowledged event after 200 ms
constexpr uint8_t kEventMaxResends = 5;        // then give up and log an error
constexpr uint64_t kOrphanUs = 5000000;        // no ping for 5 s → orphaned
constexpr uint8_t kCommandMemory = 8;          // remembered command ids (dedup)
constexpr size_t kMaxReadPerLoop = 512;        // keep loop() short

// The outside world: byte I/O and a microsecond clock.
class Io {
 public:
  virtual ~Io() {}
  virtual int read() = 0;  // next byte, or -1 if none
  virtual void write(const char* data, size_t len) = 0;
  virtual void flush() {}
  virtual uint32_t micros() = 0;
};

struct ModuleInfo {
  const char* manifestId;  // mf, e.g. "kicker/goal-sensor"
  const char* fwName;      // fw.name
  const char* fwVersion;   // fw.ver
  const char* hardware;    // hw
  const char* update;      // upd: "uf2" | "esptool" | "none"
};

enum LogLevel : char { LogDebug = 'd', LogInfo = 'i', LogWarn = 'w', LogError = 'e' };

class Core {
 public:
  typedef void (*VoidFn)();
  typedef void (*BoolFn)(bool);
  typedef void (*IdentifyFn)(uint16_t seconds);

  Core(Io& io, const ModuleInfo& info) : io_(io), info_(info) {}

  // Registers a capability instance. Call before begin(). Returns false if full or duplicate.
  bool add(Capability& cap);
  void setUid(const char* uid);

  // Sends the unsolicited announce after boot (spec 01 §5).
  void begin();
  // Reads input, answers messages, resends events. Call on every loop() iteration.
  void loop();

  // --- callbacks --------------------------------------------------------------
  void onReady(BoolFn fn) { onReady_ = fn; }         // arg: adopted
  void onOrphaned(BoolFn fn) { onOrphaned_ = fn; }   // true = server gone, false = back
  void onSleep(VoidFn fn) { onSleep_ = fn; }
  void onWake(VoidFn fn) { onWake_ = fn; }
  void onIdentify(IdentifyFn fn) { onIdentify_ = fn; }
  void onReboot(VoidFn fn) { onReboot_ = fn; }
  void onUpdate(VoidFn fn) { onUpdate_ = fn; }

  // --- outgoing ---------------------------------------------------------------
  void log(LogLevel level, const char* fmt, ...);
  void status(const char* instance, const char* s, const char* msg = nullptr);

  template <typename Fill>
  bool emit(const char* instance, const char* event, Fill fill) {
    return emitAt(instance, event, nowUs(), fill);
  }
  bool emit(const char* instance, const char* event);
  // Like emit, with an explicit module time (µs), e.g. the moment an interrupt saw the edge.
  template <typename Fill>
  bool emitAt(const char* instance, const char* event, uint64_t ts, Fill fill);
  bool emitAt(const char* instance, const char* event, uint64_t ts);

  // --- state ------------------------------------------------------------------
  bool ready() const { return ready_; }
  bool adopted() const { return adopted_; }
  bool orphaned() const { return orphaned_; }
  uint64_t nowUs() { return clock_.update(io_.micros()); }
  // Converts a raw 32-bit micros() value captured recently (e.g. in an interrupt) to module time.
  uint64_t toUs64(uint32_t micros32) {
    uint32_t m = io_.micros();
    return clock_.update(m) - uint32_t(m - micros32);
  }
  size_t pendingEvents() const;
  const char* uid() const { return uid_; }

  // For tests: the next event sequence number.
  void setNextSeq(uint32_t n) { nextSeq_ = n; }

 private:
  struct Pending {
    bool used = false;
    uint32_t n = 0;
    uint32_t order = 0;     // insertion order, to drop the oldest
    uint8_t resends = 0;
    bool sent = false;
    uint64_t lastSentUs = 0;
    uint16_t len = 0;
    char line[KM_MAX_EVENT_LEN];
  };
  struct Remembered {
    bool used = false;
    uint32_t id = 0;
    const char* nackCode = nullptr;
  };

  void handleLine(const char* line, size_t len);
  void handleHello(JsonDocument& msg);
  void handleCommand(JsonDocument& msg, uint32_t id);
  void handleConfig(JsonDocument& msg, uint32_t id);
  void handleModuleCommand(const char* t, JsonDocument& msg, uint32_t id);
  void handleAck(JsonDocument& msg);
  void handleReady(JsonDocument& msg);

  void sendAnnounce();
  void sendAckId(uint32_t id);
  void sendNack(uint32_t id, const char* code, const char* msg);
  void sendResult(uint32_t id, const Result& r);
  void sendErr(const char* code, const char* msg, bool hasRef = false, uint32_t ref = 0);
  void sendDoc(JsonDocument& doc);
  void writeLine(const char* data, size_t len);

  bool remembered(uint32_t id, Result& out) const;
  void remember(uint32_t id, const Result& r);
  Capability* find(const char* instance);

  bool queueEvent(JsonDocument& doc, uint32_t n);
  void sendPending(uint64_t now);

  Io& io_;
  ModuleInfo info_;
  char uid_[40] = {0};
  Capability* caps_[KM_MAX_CAPS] = {};
  uint8_t capCount_ = 0;

  LineBuffer<KM_MAX_LINE> in_;
  Clock64 clock_;
  bool ready_ = false;
  bool adopted_ = false;
  bool orphaned_ = false;
  uint64_t lastPingUs_ = 0;

  uint32_t nextSeq_ = 0;
  uint32_t nextOrder_ = 0;
  Pending pending_[KM_EVENT_QUEUE];
  Remembered commands_[kCommandMemory];
  uint8_t commandPos_ = 0;

  enum Deferred { None, DoReboot, DoUpdate };
  Deferred deferred_ = None;

  BoolFn onReady_ = nullptr;
  BoolFn onOrphaned_ = nullptr;
  VoidFn onSleep_ = nullptr;
  VoidFn onWake_ = nullptr;
  IdentifyFn onIdentify_ = nullptr;
  VoidFn onReboot_ = nullptr;
  VoidFn onUpdate_ = nullptr;
};

// --- templates ----------------------------------------------------------------

template <typename Fill>
bool Core::emitAt(const char* instance, const char* event, uint64_t ts, Fill fill) {
  JsonDocument doc;
  uint32_t n = nextSeq_++;  // uint32_t: wraps from 4294967295 to 0 (spec 01 §3)
  doc["t"] = "evt";
  doc["i"] = instance;
  doc["e"] = event;
  doc["n"] = n;
  doc["ts"] = ts;
  JsonObject d = doc["d"].to<JsonObject>();
  fill(d);
  if (d.size() == 0) doc.remove("d");
  return queueEvent(doc, n);
}

template <typename Fill>
bool Capability::emit(const char* event, Fill fill) {
  return core_ ? core_->emit(instance_, event, fill) : false;
}

}  // namespace kicker
