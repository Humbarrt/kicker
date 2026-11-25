#include "Core.h"

#include <stdarg.h>
#include <stdio.h>
#include <string.h>

namespace kicker {

// --- Capability helpers that need Core ------------------------------------------

bool Capability::emit(const char* event) { return core_ ? core_->emit(instance_, event) : false; }

bool Capability::emitAt(const char* event, uint64_t ts) {
  return core_ ? core_->emitAt(instance_, event, ts) : false;
}

void Capability::status(const char* s, const char* msg) {
  if (core_) core_->status(instance_, s, msg);
}

// --- setup ------------------------------------------------------------------------

bool Core::add(Capability& cap) {
  if (capCount_ >= KM_MAX_CAPS || find(cap.instance())) return false;
  cap.core_ = this;
  caps_[capCount_++] = &cap;
  return true;
}

void Core::setUid(const char* uid) {
  strncpy(uid_, uid, sizeof(uid_) - 1);
  uid_[sizeof(uid_) - 1] = '\0';
}

void Core::begin() {
  nowUs();
  for (uint8_t i = 0; i < capCount_; i++) caps_[i]->begin();
  sendAnnounce();  // once on our own after boot (spec 01 §5)
}

Capability* Core::find(const char* instance) {
  if (!instance) return nullptr;
  for (uint8_t i = 0; i < capCount_; i++) {
    if (strcmp(caps_[i]->instance(), instance) == 0) return caps_[i];
  }
  return nullptr;
}

// --- main loop --------------------------------------------------------------------

void Core::loop() {
  for (size_t i = 0; i < kMaxReadPerLoop; i++) {
    int c = io_.read();
    if (c < 0) break;
    switch (in_.feed(static_cast<char>(c))) {
      case LineBuffer<KM_MAX_LINE>::Line:
        handleLine(in_.data(), in_.length());
        break;
      case LineBuffer<KM_MAX_LINE>::TooLong:
        sendErr("too_long", "line longer than 1024 bytes");
        break;
      default:
        break;
    }
    if (deferred_ != None) break;
  }

  if (deferred_ != None) {
    Deferred d = deferred_;
    deferred_ = None;
    io_.flush();  // make sure the ack left before we reboot
    if (d == DoReboot && onReboot_) onReboot_();
    if (d == DoUpdate && onUpdate_) onUpdate_();
    return;
  }

  for (uint8_t i = 0; i < capCount_; i++) caps_[i]->update();

  uint64_t now = nowUs();
  if (ready_ && !orphaned_ && now - lastPingUs_ > kOrphanUs) {
    orphaned_ = true;
    if (onOrphaned_) onOrphaned_(true);
  }
  if (ready_ && !orphaned_) sendPending(now);
}

// --- incoming -----------------------------------------------------------------------

void Core::handleLine(const char* line, size_t len) {
  size_t start = 0;
  while (start < len && (line[start] == ' ' || line[start] == '\t')) start++;
  if (start == len || line[start] != '{') return;  // raw text from the server: ignore

  JsonDocument msg;
  if (deserializeJson(msg, line + start, len - start)) {
    sendErr("bad_json", "not valid JSON");
    return;
  }
  if (!msg.is<JsonObject>()) {
    sendErr("schema", "message must be a JSON object");
    return;
  }
  const char* t = msg["t"].as<const char*>();
  bool hasId = msg["id"].is<uint32_t>();
  uint32_t id = msg["id"].as<uint32_t>();
  if (!t) {
    sendErr("schema", "missing field t", hasId, id);
    return;
  }

  if (strcmp(t, "hello") == 0) return handleHello(msg);
  if (strcmp(t, "ready") == 0) return handleReady(msg);
  if (strcmp(t, "ack") == 0) return handleAck(msg);
  if (strcmp(t, "err") == 0) return;  // the server complained; it's in its console already

  bool needsId = strcmp(t, "ping") == 0 || strcmp(t, "cfg") == 0 || strcmp(t, "cmd") == 0 ||
                 strcmp(t, "sleep") == 0 || strcmp(t, "wake") == 0 || strcmp(t, "identify") == 0 ||
                 strcmp(t, "reboot") == 0 || strcmp(t, "update") == 0;
  if (!needsId) {
    sendErr("unknown_type", t, hasId, id);
    return;
  }
  if (!hasId) {
    sendErr("schema", "missing field id");
    return;
  }

  if (strcmp(t, "ping") == 0) {
    uint64_t now = nowUs();
    lastPingUs_ = now;
    if (orphaned_) {
      orphaned_ = false;
      if (onOrphaned_) onOrphaned_(false);
    }
    JsonDocument pong;
    pong["t"] = "pong";
    pong["id"] = id;
    pong["ts"] = now;
    sendDoc(pong);
    return;
  }
  if (strcmp(t, "cfg") == 0) return handleConfig(msg, id);
  if (strcmp(t, "cmd") == 0) return handleCommand(msg, id);
  handleModuleCommand(t, msg, id);
}

void Core::handleHello(JsonDocument& msg) {
  int v = msg["v"] | 0;
  int vmin = msg["vmin"] | 0;
  if (kProtocolVersion > v || kProtocolVersion < vmin) {
    sendErr("proto_unsupported", "module speaks protocol v1");
    return;
  }
  ready_ = false;  // a new session starts; wait for `ready`
  sendAnnounce();
}

void Core::handleReady(JsonDocument& msg) {
  ready_ = true;
  adopted_ = msg["adopted"] | false;
  if (orphaned_) {
    orphaned_ = false;
    if (onOrphaned_) onOrphaned_(false);
  }
  uint64_t now = nowUs();
  lastPingUs_ = now;
  // Everything still unacknowledged goes out again right away, with fresh retries.
  for (auto& p : pending_) {
    if (p.used) {
      p.sent = false;
      p.resends = 0;
    }
  }
  sendPending(now);
  if (onReady_) onReady_(adopted_);
  for (uint8_t i = 0; i < capCount_; i++) caps_[i]->onSync();  // state sync (spec 01 §6)
}

void Core::handleAck(JsonDocument& msg) {
  if (!msg["n"].is<uint32_t>()) {
    sendErr("schema", "ack from the server needs n");
    return;
  }
  uint32_t n = msg["n"].as<uint32_t>();
  for (auto& p : pending_) {
    if (p.used && p.n == n) p.used = false;  // equality only: wrap-around safe
  }
}

void Core::handleConfig(JsonDocument& msg, uint32_t id) {
  Result r;
  if (remembered(id, r)) return sendResult(id, r);
  const char* instance = msg["i"].as<const char*>();
  if (instance) {
    Capability* cap = find(instance);
    if (!cap) return sendErr("unknown_instance", instance, true, id);
    JsonObjectConst d = msg["d"].as<JsonObjectConst>();
    r = d.isNull() ? Result::fail(nack::BadArgs, "d must be an object") : cap->onConfig(d);
  }
  remember(id, r);
  sendResult(id, r);
}

void Core::handleCommand(JsonDocument& msg, uint32_t id) {
  Result r;
  if (remembered(id, r)) return sendResult(id, r);  // repeat: same answer, not executed again
  const char* instance = msg["i"].as<const char*>();
  const char* name = msg["c"].as<const char*>();
  if (!instance || !name) return sendErr("schema", "cmd needs i and c", true, id);
  Capability* cap = find(instance);
  if (!cap) return sendErr("unknown_instance", instance, true, id);
  r = cap->onCommand(name, msg["d"].as<JsonObjectConst>());
  remember(id, r);
  sendResult(id, r);
}

void Core::handleModuleCommand(const char* t, JsonDocument& msg, uint32_t id) {
  Result r;
  if (remembered(id, r)) return sendResult(id, r);

  if (strcmp(t, "sleep") == 0) {
    if (onSleep_) onSleep_();
  } else if (strcmp(t, "wake") == 0) {
    if (onWake_) onWake_();
  } else if (strcmp(t, "identify") == 0) {
    uint16_t s = msg["d"]["s"] | 5;
    if (onIdentify_) onIdentify_(s);
  } else if (strcmp(t, "reboot") == 0) {
    if (onReboot_) deferred_ = DoReboot;
    else r = Result::fail(nack::UnknownCommand, "reboot not supported");
  } else if (strcmp(t, "update") == 0) {
    if (onUpdate_) deferred_ = DoUpdate;
    else r = Result::fail(nack::UnknownCommand, "update not supported");
  }
  remember(id, r);
  sendResult(id, r);
}

// --- command dedup ----------------------------------------------------------------

bool Core::remembered(uint32_t id, Result& out) const {
  for (const auto& c : commands_) {
    if (c.used && c.id == id) {
      out = c.nackCode ? Result::fail(c.nackCode) : Result::ok();
      return true;
    }
  }
  return false;
}

void Core::remember(uint32_t id, const Result& r) {
  commands_[commandPos_] = Remembered{true, id, r.nackCode};
  commandPos_ = (commandPos_ + 1) % kCommandMemory;
}

// --- events -----------------------------------------------------------------------

bool Core::emit(const char* instance, const char* event) {
  return emit(instance, event, [](JsonObject) {});
}

bool Core::emitAt(const char* instance, const char* event, uint64_t ts) {
  return emitAt(instance, event, ts, [](JsonObject) {});
}

size_t Core::pendingEvents() const {
  size_t n = 0;
  for (const auto& p : pending_) n += p.used ? 1 : 0;
  return n;
}

bool Core::queueEvent(JsonDocument& doc, uint32_t n) {
  size_t len = measureJson(doc);
  if (len + 1 > KM_MAX_EVENT_LEN) {
    log(LogError, "event dropped: %u bytes, max %u", unsigned(len), unsigned(KM_MAX_EVENT_LEN - 1));
    return false;
  }
  Pending* slot = nullptr;
  for (auto& p : pending_) {
    if (!p.used) {
      slot = &p;
      break;
    }
  }
  bool dropped = false;
  if (!slot) {
    slot = &pending_[0];
    for (auto& p : pending_) {
      if (p.order < slot->order) slot = &p;
    }
    log(LogWarn, "event queue full, dropped event n=%lu", (unsigned long)slot->n);
    dropped = true;
  }
  slot->used = true;
  slot->n = n;
  slot->order = nextOrder_++;
  slot->resends = 0;
  slot->sent = false;
  slot->len = static_cast<uint16_t>(serializeJson(doc, slot->line, sizeof(slot->line)));
  if (ready_ && !orphaned_) sendPending(nowUs());
  return !dropped;
}

void Core::sendPending(uint64_t now) {
  // Oldest first, so the server sees events in order.
  bool done[KM_EVENT_QUEUE] = {false};
  for (size_t round = 0; round < KM_EVENT_QUEUE; round++) {
    int next = -1;
    for (size_t i = 0; i < KM_EVENT_QUEUE; i++) {
      if (pending_[i].used && !done[i] && (next < 0 || pending_[i].order < pending_[next].order)) {
        next = static_cast<int>(i);
      }
    }
    if (next < 0) return;
    done[next] = true;
    Pending& p = pending_[next];
    if (!p.sent) {
      writeLine(p.line, p.len);
      p.sent = true;
      p.lastSentUs = now;
    } else if (now - p.lastSentUs >= kEventRetryUs) {
      if (p.resends >= kEventMaxResends) {
        p.used = false;
        log(LogError, "event n=%lu was never acknowledged, dropped", (unsigned long)p.n);
        continue;
      }
      writeLine(p.line, p.len);
      p.resends++;
      p.lastSentUs = now;
    }
  }
}

// --- outgoing ---------------------------------------------------------------------

void Core::sendAnnounce() {
  JsonDocument doc;
  doc["t"] = "announce";
  doc["v"] = kProtocolVersion;
  doc["uid"] = uid_;
  doc["ts"] = nowUs();  // lets the server tell a reboot from a repeated announce
  JsonObject fw = doc["fw"].to<JsonObject>();
  fw["name"] = info_.fwName;
  fw["ver"] = info_.fwVersion;
  doc["mf"] = info_.manifestId;
  if (info_.hardware && *info_.hardware) doc["hw"] = info_.hardware;
  if (info_.update && *info_.update) doc["upd"] = info_.update;
  JsonArray caps = doc["caps"].to<JsonArray>();
  for (uint8_t i = 0; i < capCount_; i++) {
    Capability* c = caps_[i];
    JsonObject e = caps.add<JsonObject>();
    e["i"] = c->instance();
    e["c"] = c->id();
    e["cv"] = c->version();
    JsonObject p = e["p"].to<JsonObject>();
    if (!c->writeParams(p)) e.remove("p");
    if (c->canWake()) e["w"] = 1;
  }
  sendDoc(doc);
}

void Core::log(LogLevel level, const char* fmt, ...) {
  char text[200];
  va_list args;
  va_start(args, fmt);
  vsnprintf(text, sizeof(text), fmt, args);
  va_end(args);
  const char l[2] = {static_cast<char>(level), '\0'};
  JsonDocument doc;
  doc["t"] = "log";
  doc["l"] = l;
  doc["m"] = text;
  sendDoc(doc);
}

void Core::status(const char* instance, const char* s, const char* msg) {
  JsonDocument doc;
  doc["t"] = "status";
  if (instance) doc["i"] = instance;
  doc["s"] = s;
  if (msg) doc["msg"] = msg;
  sendDoc(doc);
}

void Core::sendAckId(uint32_t id) {
  JsonDocument doc;
  doc["t"] = "ack";
  doc["id"] = id;
  sendDoc(doc);
}

void Core::sendNack(uint32_t id, const char* code, const char* msg) {
  JsonDocument doc;
  doc["t"] = "nack";
  doc["id"] = id;
  doc["code"] = code;
  if (msg) doc["msg"] = msg;
  sendDoc(doc);
}

void Core::sendResult(uint32_t id, const Result& r) {
  if (r.isOk()) sendAckId(id);
  else sendNack(id, r.nackCode, r.message);
}

void Core::sendErr(const char* code, const char* msg, bool hasRef, uint32_t ref) {
  JsonDocument doc;
  doc["t"] = "err";
  doc["code"] = code;
  doc["msg"] = msg;
  if (hasRef) doc["ref"] = ref;
  sendDoc(doc);
}

void Core::sendDoc(JsonDocument& doc) {
  if (measureJson(doc) + 1 > KM_MAX_LINE) {
    static const char tooLong[] = "{\"t\":\"log\",\"l\":\"e\",\"m\":\"outgoing message too long, dropped\"}";
    writeLine(tooLong, sizeof(tooLong) - 1);
    return;
  }
  char buf[KM_MAX_LINE];
  size_t n = serializeJson(doc, buf, sizeof(buf));
  writeLine(buf, n);
}

void Core::writeLine(const char* data, size_t len) {
  io_.write(data, len);
  io_.write("\n", 1);
}

}  // namespace kicker
