// Native unit tests for BeamDetector (goal detection logic). Run: pio test -e native
#include <unity.h>

#include "kicker/BeamDetector.h"

using kicker::BeamDetector;

static BeamDetector d;
static const uint64_t T0 = 5000000;  // 5 s after boot

void setUp() { d = BeamDetector(); }
void tearDown() {}

void test_short_blip_is_noise() {
  TEST_ASSERT_EQUAL(BeamDetector::None, d.edge(T0, true));
  TEST_ASSERT_EQUAL(BeamDetector::None, d.poll(T0 + 100));
  TEST_ASSERT_EQUAL(BeamDetector::None, d.edge(T0 + 200, false));  // 200 µs < 300 µs
  TEST_ASSERT_EQUAL(BeamDetector::None, d.poll(T0 + 10000));
}

void test_goal_reported_while_blocked_with_start_time() {
  d.edge(T0, true);
  TEST_ASSERT_EQUAL(BeamDetector::None, d.poll(T0 + 299));
  TEST_ASSERT_EQUAL(BeamDetector::Goal, d.poll(T0 + 300));
  TEST_ASSERT_EQUAL_UINT64(T0, d.goalTime());
  TEST_ASSERT_EQUAL(BeamDetector::None, d.poll(T0 + 5000));      // only once
  TEST_ASSERT_EQUAL(BeamDetector::None, d.edge(T0 + 20000, false));  // not again on release
}

void test_goal_on_release_if_poll_was_late() {
  d.edge(T0, true);
  TEST_ASSERT_EQUAL(BeamDetector::Goal, d.edge(T0 + 15000, false));  // loop didn't poll in between
  TEST_ASSERT_EQUAL_UINT64(T0, d.goalTime());
}

void test_lockout_ignores_bounce() {
  d.edge(T0, true);
  TEST_ASSERT_EQUAL(BeamDetector::Goal, d.poll(T0 + 1000));
  d.edge(T0 + 20000, false);
  // same ball bouncing back through the gate 400 ms later
  d.edge(T0 + 400000, true);
  TEST_ASSERT_EQUAL(BeamDetector::None, d.poll(T0 + 401000));
  d.edge(T0 + 420000, false);
  // next real goal 1.2 s after the first
  d.edge(T0 + 1200000, true);
  TEST_ASSERT_EQUAL(BeamDetector::Goal, d.poll(T0 + 1201000));
  TEST_ASSERT_EQUAL_UINT64(T0 + 1200000, d.goalTime());
}

void test_stuck_ball_faults_and_recovers_without_goal() {
  BeamDetector::Config c;
  c.lockoutMs = 0;
  d.configure(c);
  d.edge(T0, true);
  TEST_ASSERT_EQUAL(BeamDetector::Goal, d.poll(T0 + 1000));  // it did look like a goal at first
  TEST_ASSERT_EQUAL(BeamDetector::None, d.poll(T0 + 9999000));
  TEST_ASSERT_EQUAL(BeamDetector::Fault, d.poll(T0 + 10000000));
  TEST_ASSERT_TRUE(d.faulted());
  TEST_ASSERT_EQUAL(BeamDetector::None, d.poll(T0 + 20000000));  // fault reported once
  TEST_ASSERT_EQUAL(BeamDetector::Recovered, d.edge(T0 + 30000000, false));
  TEST_ASSERT_FALSE(d.faulted());
}

void test_blocked_at_boot_is_no_goal_but_faults() {
  BeamDetector::Config c;
  c.faultMs = 2000;
  d.configure(c);
  d.reset(0, true);  // the firmware sets the initial level at boot
  for (uint64_t t = 0; t < 1999000; t += 1000) TEST_ASSERT_EQUAL(BeamDetector::None, d.poll(t));
  TEST_ASSERT_EQUAL(BeamDetector::Fault, d.poll(2000000));
  TEST_ASSERT_EQUAL(BeamDetector::Recovered, d.edge(2500000, false));
  d.edge(3000000, true);  // the next real ball counts again
  TEST_ASSERT_EQUAL(BeamDetector::Goal, d.poll(3001000));
}

void test_clear_at_boot() {
  d.reset(0, false);
  TEST_ASSERT_EQUAL(BeamDetector::None, d.poll(5000000));
  d.edge(T0, true);
  TEST_ASSERT_EQUAL(BeamDetector::Goal, d.poll(T0 + 300));
}

void test_blocked_at_boot_cleared_quickly() {
  d.reset(0, true);
  TEST_ASSERT_EQUAL(BeamDetector::None, d.edge(400000, false));  // hand removed after 0.4 s: nothing
  TEST_ASSERT_FALSE(d.faulted());
}

void test_repeated_edges_are_ignored() {
  d.edge(T0, true);
  TEST_ASSERT_EQUAL(BeamDetector::None, d.edge(T0 + 50, true));  // bounce on the edge, same level
  TEST_ASSERT_EQUAL(BeamDetector::Goal, d.poll(T0 + 300));       // measured from the first edge
}

void test_config_change_applies() {
  BeamDetector::Config c;
  c.minBlockUs = 2000;
  d.configure(c);
  d.edge(T0, true);
  TEST_ASSERT_EQUAL(BeamDetector::None, d.poll(T0 + 1999));
  TEST_ASSERT_EQUAL(BeamDetector::Goal, d.poll(T0 + 2000));
}

// Every finished interruption is kept with its duration and verdict (bench rig diagnostics).
void test_blocks_report_duration_and_verdict() {
  BeamDetector::Block b;
  TEST_ASSERT_FALSE(d.takeBlock(b));

  d.edge(T0, true);
  d.edge(T0 + 200, false);  // noise
  TEST_ASSERT_TRUE(d.takeBlock(b));
  TEST_ASSERT_EQUAL_UINT32(200, b.us);
  TEST_ASSERT_EQUAL(BeamDetector::Noise, b.verdict);
  TEST_ASSERT_FALSE(d.takeBlock(b));  // only once

  d.edge(T0 + 1000, true);
  d.poll(T0 + 2000);  // goal while blocked
  d.edge(T0 + 12800, false);
  TEST_ASSERT_TRUE(d.takeBlock(b));
  TEST_ASSERT_EQUAL_UINT32(11800, b.us);
  TEST_ASSERT_EQUAL(BeamDetector::Counted, b.verdict);

  d.edge(T0 + 300000, true);  // bounce within the lockout
  d.edge(T0 + 309000, false);
  TEST_ASSERT_TRUE(d.takeBlock(b));
  TEST_ASSERT_EQUAL(BeamDetector::Lockout, b.verdict);

  d.edge(T0 + 3000000, true);  // goal decided only on release (late poll)
  TEST_ASSERT_EQUAL(BeamDetector::Goal, d.edge(T0 + 3015000, false));
  TEST_ASSERT_TRUE(d.takeBlock(b));
  TEST_ASSERT_EQUAL(BeamDetector::Counted, b.verdict);
  TEST_ASSERT_EQUAL_UINT32(15000, b.us);

  d.edge(T0 + 5000000, true);  // stuck
  d.poll(T0 + 5001000);
  d.poll(T0 + 16000000);
  d.edge(T0 + 17000000, false);
  TEST_ASSERT_TRUE(d.takeBlock(b));
  TEST_ASSERT_EQUAL(BeamDetector::Stuck, b.verdict);
}

void test_block_at_startup() {
  BeamDetector::Block b;
  d.reset(T0, true);
  d.edge(T0 + 50000, false);
  TEST_ASSERT_TRUE(d.takeBlock(b));
  TEST_ASSERT_EQUAL(BeamDetector::AtStartup, b.verdict);
  d.edge(T0 + 60000, true);
  d.edge(T0 + 60100, false);
  TEST_ASSERT_TRUE(d.takeBlock(b));
  TEST_ASSERT_EQUAL(BeamDetector::Noise, b.verdict);
}

int main(int, char**) {
  UNITY_BEGIN();
  RUN_TEST(test_short_blip_is_noise);
  RUN_TEST(test_goal_reported_while_blocked_with_start_time);
  RUN_TEST(test_goal_on_release_if_poll_was_late);
  RUN_TEST(test_lockout_ignores_bounce);
  RUN_TEST(test_stuck_ball_faults_and_recovers_without_goal);
  RUN_TEST(test_blocked_at_boot_is_no_goal_but_faults);
  RUN_TEST(test_clear_at_boot);
  RUN_TEST(test_blocked_at_boot_cleared_quickly);
  RUN_TEST(test_repeated_edges_are_ignored);
  RUN_TEST(test_config_change_applies);
  RUN_TEST(test_blocks_report_duration_and_verdict);
  RUN_TEST(test_block_at_startup);
  return UNITY_END();
}
