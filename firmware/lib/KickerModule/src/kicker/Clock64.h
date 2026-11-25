#pragma once
#include <stdint.h>

namespace kicker {

// Extends a 32-bit microsecond counter (Arduino micros() wraps after ~71 minutes)
// to 64 bits. update() must be called at least once per wrap period – loop() does.
class Clock64 {
 public:
  uint64_t update(uint32_t now) {
    if (init_ && now < last_) high_ += (uint64_t(1) << 32);
    last_ = now;
    init_ = true;
    return high_ | now;
  }

 private:
  uint32_t last_ = 0;
  uint64_t high_ = 0;
  bool init_ = false;
};

}  // namespace kicker
