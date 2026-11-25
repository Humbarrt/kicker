#pragma once
#include <stddef.h>

namespace kicker {

// Collects bytes into lines of at most N-1 characters (spec 01 §2: 1024 bytes including '\n').
// Over-long lines are skipped up to the next newline and reported once as TooLong.
template <size_t N>
class LineBuffer {
 public:
  enum Result { None, Line, TooLong };

  Result feed(char c) {
    if (complete_) {
      len_ = 0;
      complete_ = false;
    }
    if (c == '\n') {
      if (overflow_) {
        overflow_ = false;
        len_ = 0;
        return TooLong;
      }
      if (len_ > 0 && buf_[len_ - 1] == '\r') len_--;
      if (len_ == 0) return None;  // ignore empty lines
      buf_[len_] = '\0';
      complete_ = true;
      return Line;
    }
    if (overflow_) return None;
    if (len_ >= N - 1) {
      overflow_ = true;
      return None;
    }
    buf_[len_++] = c;
    return None;
  }

  // Valid after feed() returned Line, until the next feed().
  const char* data() const { return buf_; }
  size_t length() const { return len_; }

 private:
  char buf_[N];
  size_t len_ = 0;
  bool overflow_ = false;
  bool complete_ = false;
};

}  // namespace kicker
