#pragma once
#include <stdint.h>

namespace kicker {

// Turns the raw edges of a light gate into goals (spec 01, goal_sensor@1). Hardware-free, so it
// can be unit-tested; the firmware feeds it interrupt-timestamped edges and polls it in loop().
//
//   - blocked shorter than minBlockUs     → noise, ignored
//   - blocked for minBlockUs              → goal, timestamped at the start of the block
//                                           (reported while still blocked, for low latency)
//   - another block within lockoutMs      → ignored (same ball bouncing in the tube)
//   - blocked for faultMs                 → fault (ball stuck / sensor misaligned); no goal when it clears
//   - already blocked at start-up (reset) → no goal; fault if it stays blocked
//
// Every finished interruption is also kept as a Block (duration + verdict) for diagnostics: the
// bench rig and the module console show why a ball did or didn't count.
class BeamDetector {
 public:
  enum Verdict : uint8_t { Noise, Counted, Lockout, Stuck, AtStartup };
  struct Block {
    uint32_t us;  // how long the beam was blocked (capped at ~71 min)
    Verdict verdict;
  };

  struct Config {
    uint32_t minBlockUs = 300;
    uint32_t lockoutMs = 1000;
    uint32_t faultMs = 10000;
  };
  enum Event { None, Goal, Fault, Recovered };

  void configure(const Config& c) { config_ = c; }

  // Sets the level at start-up without producing a goal: a beam that is already blocked at
  // power-on is not a goal, it only becomes a fault if it stays blocked for faultMs.
  void reset(uint64_t t, bool blocked) {
    blocked_ = blocked;
    blockStart_ = t;
    handled_ = true;
    faulted_ = false;
    startup_ = blocked;
    hasBlock_ = false;
  }
  const Config& config() const { return config_; }

  // An edge: at time t (µs) the beam became blocked (true) or clear (false).
  Event edge(uint64_t t, bool blocked) {
    if (blocked == blocked_) return None;
    blocked_ = blocked;
    if (blocked) {
      blockStart_ = t;
      handled_ = false;
      startup_ = false;
      return None;
    }
    uint64_t held = t - blockStart_;
    block_.us = held > 0xFFFFFFFFull ? 0xFFFFFFFFu : uint32_t(held);
    hasBlock_ = true;
    if (faulted_) {
      faulted_ = false;
      block_.verdict = Stuck;
      return Recovered;
    }
    if (startup_) {
      startup_ = false;
      block_.verdict = AtStartup;
      return None;
    }
    if (!handled_ && held >= config_.minBlockUs) {
      Event e = goal();
      block_.verdict = verdict_;
      return e;
    }
    block_.verdict = handled_ ? verdict_ : Noise;
    return None;
  }

  // takeBlock returns the last finished interruption once (for diagnostics).
  bool takeBlock(Block& out) {
    if (!hasBlock_) return false;
    out = block_;
    hasBlock_ = false;
    return true;
  }

  // Time-based checks; call regularly with the current time (µs).
  Event poll(uint64_t now) {
    if (!blocked_) return None;
    uint64_t held = now - blockStart_;
    if (!handled_ && held >= config_.minBlockUs) return goal();
    if (!faulted_ && held >= uint64_t(config_.faultMs) * 1000) {
      faulted_ = true;
      return Fault;
    }
    return None;
  }

  bool blocked() const { return blocked_; }
  bool faulted() const { return faulted_; }
  // Start of the block that produced the last Goal (µs).
  uint64_t goalTime() const { return lastGoal_; }

 private:
  Event goal() {
    handled_ = true;
    if (haveGoal_ && blockStart_ - lastGoal_ < uint64_t(config_.lockoutMs) * 1000) {
      verdict_ = Lockout;
      return None;
    }
    haveGoal_ = true;
    lastGoal_ = blockStart_;
    verdict_ = Counted;
    return Goal;
  }

  Config config_;
  bool blocked_ = false;
  bool handled_ = false;  // this block was already evaluated (goal or lockout)
  bool faulted_ = false;
  bool haveGoal_ = false;
  bool startup_ = false;  // the current block began before reset()
  bool hasBlock_ = false;
  Verdict verdict_ = Noise;  // of the current block, once handled
  Block block_ = {0, Noise};
  uint64_t blockStart_ = 0;
  uint64_t lastGoal_ = 0;
};

}  // namespace kicker
