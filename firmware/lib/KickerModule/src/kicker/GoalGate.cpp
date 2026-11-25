#ifdef ARDUINO
#include "GoalGate.h"

#include "Core.h"

namespace kicker {

static const char kFaultMsg[] = "beam blocked for 10 s: ball stuck, sensor misaligned or wrong broken level";

// attachInterrupt() takes a plain function, so each gate gets a slot with its own trampoline.
static GoalGate* gSlots[GoalGate::kMaxGates];

template <int I>
static void isrSlot() {
  if (gSlots[I]) gSlots[I]->onEdge();
}

static void (*const kIsr[GoalGate::kMaxGates])() = {
    isrSlot<0>, isrSlot<1>, isrSlot<2>, isrSlot<3>, isrSlot<4>, isrSlot<5>, isrSlot<6>, isrSlot<7>,
};

void GoalGate::begin() {
  pinMode(pin_, INPUT_PULLUP);
  applyConfig();
  lastPolled_ = blockedNow();
  if (core()) detector_.reset(core()->nowUs(), lastPolled_);  // initial level: no goal, may fault later

  for (uint8_t i = 0; i < kMaxGates; i++) {
    if (!gSlots[i]) {
      gSlots[i] = this;
      slot_ = static_cast<int8_t>(i);
      attachInterrupt(digitalPinToInterrupt(pin_), kIsr[i], CHANGE);
      return;
    }
  }
  // no free slot: update() polls the pin instead (timestamps then depend on loop() speed)
}

void GoalGate::onEdge() {
  uint8_t h = head_;
  times_[h % kRing] = micros();
  blocked_[h % kRing] = blockedNow();
  head_ = h + 1;
}

void GoalGate::update() {
  Core* c = core();
  if (!c) return;

  if (slot_ < 0) {
    bool b = blockedNow();
    if (b != lastPolled_) {
      lastPolled_ = b;
      report(detector_.edge(c->nowUs(), b));
    }
  } else {
    uint8_t head = head_;
    if (uint8_t(head - tail_) > kRing) {  // loop() stalled for far too long
      c->log(LogWarn, "gate %s: %u edges lost", instance(), unsigned(uint8_t(head - tail_) - kRing));
      tail_ = head - kRing;
    }
    while (tail_ != head) {
      uint8_t k = tail_ % kRing;
      report(detector_.edge(c->toUs64(times_[k]), blocked_[k]));
      trace(c->nowUs());
      tail_++;
    }
  }
  report(detector_.poll(c->nowUs()));
  trace(c->nowUs());
}

void GoalGate::trace(uint64_t now) {
  BeamDetector::Block b;
  if (!detector_.takeBlock(b)) return;
  uint32_t sec = uint32_t(now / 1000000);
  if (sec != traceSec_) {
    traceSec_ = sec;
    traceCount_ = 0;
  }
  if (traceCount_ >= kTracePerSec) {
    if (suppressed_ < 0xFFFF) suppressed_++;
    return;
  }
  traceCount_++;
  static const char* const kVerdict[] = {"noise", "counted", "lockout", "stuck", "at startup"};
  const char* why = b.verdict <= BeamDetector::AtStartup ? kVerdict[b.verdict] : "?";
  unsigned long ms = b.us / 1000, frac = (b.us % 1000) / 10;
  if (suppressed_) {
    core()->log(LogDebug, "gate %s: blocked %lu.%02lu ms -> %s (%u more not shown)", instance(), ms, frac, why,
                unsigned(suppressed_));
    suppressed_ = 0;
  } else {
    core()->log(LogDebug, "gate %s: blocked %lu.%02lu ms -> %s", instance(), ms, frac, why);
  }
}

void GoalGate::report(BeamDetector::Event ev) {
  switch (ev) {
    case BeamDetector::Goal:
      goal(detector_.goalTime());
      break;
    case BeamDetector::Fault:
      fault(kFaultMsg);
      break;
    case BeamDetector::Recovered:
      ok();
      break;
    default:
      break;
  }
}

void GoalGate::onSync() {
  // Status isn't queued while no server listens, so report the current one on every connect.
  if (detector_.faulted()) fault(kFaultMsg);
  else ok();
  // Shown in the module console after every connect – the quickest wiring / polarity check:
  // with nothing in the tube every gate must say "clear".
  if (core()) {
    core()->log(LogInfo, "gate %s (pin %u): %s, broken level %s%s", instance(), unsigned(pin_),
                blockedNow() ? "BLOCKED" : "clear", brokenLevel_ == LOW ? "LOW" : "HIGH",
                slot_ < 0 ? ", polling" : "");
  }
}

Result GoalGate::onConfig(JsonObjectConst d) {
  Result r = GoalSensor::onConfig(d);
  if (r.isOk()) applyConfig();
  return r;
}

void GoalGate::applyConfig() {
  BeamDetector::Config d = detector_.config();
  d.lockoutMs = config().lockoutMs;
  d.minBlockUs = config().minBlockUs;
  detector_.configure(d);
}

}  // namespace kicker
#endif  // ARDUINO
