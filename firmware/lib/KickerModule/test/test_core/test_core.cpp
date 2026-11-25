// Native unit tests for the protocol core (spec 01). Run: pio test -e native
#include <ArduinoJson.h>
#include <unity.h>

#include <deque>
#include <string>
#include <vector>

#include "kicker/Clock64.h"
#include "kicker/Core.h"
#include "kicker/GoalSensor.h"
#include "kicker/LineBuffer.h"

using namespace kicker;

// --- fake I/O ---------------------------------------------------------------------

class FakeIo : public Io {
 public:
  std::deque<char> input;
  std::string output;
  uint32_t now = 1000;

  int read() override {
    if (input.empty()) return -1;
    char c = input.front();
    input.pop_front();
    return static_cast<unsigned char>(c);
  }
  void write(const char* data, size_t len) override { output.append(data, len); }
  uint32_t micros() override { return now; }

  void send(const std::string& line) {
    for (char c : line + "\n") input.push_back(c);
  }
  // Returns and clears all complete output lines.
  std::vector<std::string> lines() {
    std::vector<std::string> out;
    size_t start = 0, nl;
    while ((nl = output.find('\n', start)) != std::string::npos) {
      out.push_back(output.substr(start, nl - start));
      start = nl + 1;
    }
    output.clear();
    return out;
  }
};

static JsonDocument parse(const std::string& line) {
  JsonDocument doc;
  TEST_ASSERT_FALSE_MESSAGE(deserializeJson(doc, line), line.c_str());
  return doc;
}

static std::vector<JsonDocument> ofType(const std::vector<std::string>& lines, const char* t) {
  std::vector<JsonDocument> out;
  for (const auto& l : lines) {
    JsonDocument d = parse(l);
    if (strcmp(d["t"] | "", t) == 0) out.push_back(d);
  }
  return out;
}

// --- a custom capability for command tests ------------------------------------------

class Counter : public Capability {
 public:
  int calls = 0;
  int begins = 0;
  int updates = 0;
  bool synced = false;
  void begin() override { begins++; }
  void update() override { updates++; }
  Counter() : Capability("x", "vendor.test/counter", 1) {}
  Result onCommand(const char* name, JsonObjectConst args) override {
    if (strcmp(name, "inc") != 0) return Result::fail(nack::UnknownCommand);
    if (!args["by"].is<int>()) return Result::fail(nack::BadArgs, "by missing");
    calls += args["by"].as<int>();
    return Result::ok();
  }
  bool writeParams(JsonObject p) override {
    p["max"] = 9;
    return true;
  }
  void onSync() override { synced = true; }
};

// --- fixture ----------------------------------------------------------------------

static FakeIo* io;
static Core* core;
static GoalSensor* goalA;
static GoalSensor* goalB;
static Counter* counter;

static const ModuleInfo kInfo = {"kicker/goal-sensor", "kicker-goal-sensor", "0.2.0", "rp2040-zero", "uf2"};

void setUp() {
  io = new FakeIo();
  core = new Core(*io, kInfo);
  goalA = new GoalSensor("a");
  goalB = new GoalSensor("b", true);
  counter = new Counter();
  core->add(*goalA);
  core->add(*goalB);
  core->add(*counter);
  core->setUid("e66141040b3c2a29");
  core->begin();
}

void tearDown() {
  delete counter;
  delete goalB;
  delete goalA;
  delete core;
  delete io;
}

static void handshake(bool adopted = true) {
  io->send(R"({"t":"hello","v":1,"vmin":1,"st":0})");
  core->loop();
  io->send(std::string(R"({"t":"ready","sid":"s1","adopted":)") + (adopted ? "true" : "false") + "}");
  core->loop();
}

static void advance(uint32_t us) {
  io->now += us;
  core->loop();
}

// --- tests ------------------------------------------------------------------------

void test_announces_after_boot() {
  auto lines = io->lines();
  TEST_ASSERT_EQUAL(1, lines.size());
  JsonDocument a = parse(lines[0]);
  TEST_ASSERT_EQUAL_STRING("announce", a["t"]);
  TEST_ASSERT_EQUAL(1, a["v"].as<int>());
  TEST_ASSERT_EQUAL_STRING("e66141040b3c2a29", a["uid"]);
  TEST_ASSERT_EQUAL(1000, a["ts"].as<uint64_t>());
  TEST_ASSERT_EQUAL_STRING("kicker-goal-sensor", a["fw"]["name"]);
  TEST_ASSERT_EQUAL_STRING("0.2.0", a["fw"]["ver"]);
  TEST_ASSERT_EQUAL_STRING("kicker/goal-sensor", a["mf"]);
  TEST_ASSERT_EQUAL_STRING("uf2", a["upd"]);
  TEST_ASSERT_EQUAL(3, a["caps"].size());
  TEST_ASSERT_EQUAL_STRING("goal_sensor", a["caps"][0]["c"]);
  TEST_ASSERT_TRUE(a["caps"][0]["w"].isNull());
  TEST_ASSERT_EQUAL(1, a["caps"][1]["w"].as<int>());
  TEST_ASSERT_TRUE(a["caps"][0]["p"].isNull());
  TEST_ASSERT_EQUAL(9, a["caps"][2]["p"]["max"].as<int>());
}

void test_hello_gets_announce() {
  io->lines();
  io->send(R"({"t":"hello","v":1,"vmin":1,"st":123})");
  core->loop();
  TEST_ASSERT_EQUAL(1, ofType(io->lines(), "announce").size());
}

void test_hello_incompatible_version() {
  io->lines();
  io->send(R"({"t":"hello","v":3,"vmin":2,"st":0})");
  core->loop();
  auto errs = ofType(io->lines(), "err");
  TEST_ASSERT_EQUAL(1, errs.size());
  TEST_ASSERT_EQUAL_STRING("proto_unsupported", errs[0]["code"]);
}

void test_ping_pong() {
  handshake();
  io->lines();
  io->now = 5321443;
  io->send(R"({"t":"ping","id":2})");
  core->loop();
  auto pongs = ofType(io->lines(), "pong");
  TEST_ASSERT_EQUAL(1, pongs.size());
  TEST_ASSERT_EQUAL(2, pongs[0]["id"].as<uint32_t>());
  TEST_ASSERT_EQUAL(5321443, pongs[0]["ts"].as<uint64_t>());
}

void test_event_before_ready_is_queued() {
  io->lines();
  goalA->goal();
  TEST_ASSERT_EQUAL(0, ofType(io->lines(), "evt").size());
  TEST_ASSERT_EQUAL(1, core->pendingEvents());
  handshake();
  auto evts = ofType(io->lines(), "evt");
  TEST_ASSERT_EQUAL(1, evts.size());
  TEST_ASSERT_EQUAL_STRING("a", evts[0]["i"]);
  TEST_ASSERT_EQUAL_STRING("goal", evts[0]["e"]);
  TEST_ASSERT_EQUAL(0, evts[0]["n"].as<uint32_t>());
  TEST_ASSERT_TRUE(evts[0]["d"].isNull());
}

void test_event_acked_is_not_resent() {
  handshake();
  io->lines();
  goalA->goal();
  TEST_ASSERT_EQUAL(1, ofType(io->lines(), "evt").size());
  io->send(R"({"t":"ack","n":0})");
  core->loop();
  TEST_ASSERT_EQUAL(0, core->pendingEvents());
  advance(1000000);
  TEST_ASSERT_EQUAL(0, ofType(io->lines(), "evt").size());
}

void test_event_retry_then_give_up() {
  handshake();
  io->lines();
  goalA->goal();
  io->lines();
  for (int i = 0; i < 5; i++) {
    io->send(R"({"t":"ping","id":1})");  // keep the session alive
    advance(200000);
    auto evts = ofType(io->lines(), "evt");
    TEST_ASSERT_EQUAL_MESSAGE(1, evts.size(), "resend expected");
    TEST_ASSERT_EQUAL(0, evts[0]["n"].as<uint32_t>());  // same n
  }
  io->send(R"({"t":"ping","id":1})");
  advance(200000);
  auto lines = io->lines();
  TEST_ASSERT_EQUAL(0, ofType(lines, "evt").size());
  auto logs = ofType(lines, "log");
  TEST_ASSERT_EQUAL(1, logs.size());
  TEST_ASSERT_EQUAL_STRING("e", logs[0]["l"]);
  TEST_ASSERT_EQUAL(0, core->pendingEvents());
}

static bool g_orphaned;

void test_orphaned_pauses_retries_and_recovers() {
  g_orphaned = false;
  core->onOrphaned([](bool o) { g_orphaned = o; });
  handshake();
  goalA->goal();
  io->lines();
  advance(5100000);  // no ping for > 5 s
  TEST_ASSERT_TRUE(core->orphaned());
  TEST_ASSERT_TRUE(g_orphaned);
  io->lines();
  advance(1000000);
  TEST_ASSERT_EQUAL(0, ofType(io->lines(), "evt").size());  // no retries while orphaned
  TEST_ASSERT_EQUAL(1, core->pendingEvents());              // but the goal isn't lost
  // the server comes back with a new session
  handshake();
  TEST_ASSERT_FALSE(core->orphaned());
  TEST_ASSERT_EQUAL(1, ofType(io->lines(), "evt").size());
  TEST_ASSERT_FALSE(g_orphaned);
}

void test_sequence_wraps_around() {
  handshake();
  io->lines();
  core->setNextSeq(4294967295u);
  goalA->goal();
  goalA->goal();
  auto evts = ofType(io->lines(), "evt");
  TEST_ASSERT_EQUAL(2, evts.size());
  TEST_ASSERT_EQUAL_UINT32(4294967295u, evts[0]["n"].as<uint32_t>());
  TEST_ASSERT_EQUAL_UINT32(0, evts[1]["n"].as<uint32_t>());
  io->send(R"({"t":"ack","n":4294967295})");
  io->send(R"({"t":"ack","n":0})");
  core->loop();
  TEST_ASSERT_EQUAL(0, core->pendingEvents());
}

void test_queue_overflow_drops_oldest() {
  io->lines();
  for (int i = 0; i < KM_EVENT_QUEUE; i++) TEST_ASSERT_TRUE(goalA->goal());
  TEST_ASSERT_FALSE(goalA->goal());  // 9th event pushes out n=0
  auto logs = ofType(io->lines(), "log");
  TEST_ASSERT_EQUAL(1, logs.size());
  TEST_ASSERT_EQUAL_STRING("w", logs[0]["l"]);
  handshake();
  auto evts = ofType(io->lines(), "evt");
  TEST_ASSERT_EQUAL(KM_EVENT_QUEUE, evts.size());
  TEST_ASSERT_EQUAL(1, evts[0]["n"].as<uint32_t>());  // oldest remaining, in order
  TEST_ASSERT_EQUAL(8, evts[7]["n"].as<uint32_t>());
}

void test_command_dispatch_and_dedup() {
  handshake();
  io->lines();
  io->send(R"({"t":"cmd","id":7,"i":"x","c":"inc","d":{"by":2}})");
  io->send(R"({"t":"cmd","id":7,"i":"x","c":"inc","d":{"by":2}})");  // server retry
  core->loop();
  TEST_ASSERT_EQUAL(2, counter->calls);  // executed once
  auto acks = ofType(io->lines(), "ack");
  TEST_ASSERT_EQUAL(2, acks.size());  // answered twice
  TEST_ASSERT_EQUAL(7, acks[1]["id"].as<uint32_t>());
}

void test_command_nacks_and_errors() {
  handshake();
  io->lines();
  io->send(R"({"t":"cmd","id":1,"i":"x","c":"dance"})");
  io->send(R"({"t":"cmd","id":2,"i":"x","c":"inc","d":{}})");
  io->send(R"({"t":"cmd","id":3,"i":"nope","c":"inc"})");
  core->loop();
  auto lines = io->lines();
  auto nacks = ofType(lines, "nack");
  TEST_ASSERT_EQUAL(2, nacks.size());
  TEST_ASSERT_EQUAL_STRING("unknown_command", nacks[0]["code"]);
  TEST_ASSERT_EQUAL_STRING("bad_args", nacks[1]["code"]);
  TEST_ASSERT_EQUAL_STRING("by missing", nacks[1]["msg"]);
  auto errs = ofType(lines, "err");
  TEST_ASSERT_EQUAL(1, errs.size());
  TEST_ASSERT_EQUAL_STRING("unknown_instance", errs[0]["code"]);
  TEST_ASSERT_EQUAL(3, errs[0]["ref"].as<uint32_t>());
}

void test_goal_sensor_partial_config() {
  handshake();
  io->lines();
  io->send(R"({"t":"cfg","id":1,"i":"a","d":{"lockout_ms":1500}})");
  core->loop();
  TEST_ASSERT_EQUAL(1500, goalA->config().lockoutMs);
  TEST_ASSERT_EQUAL(300, goalA->config().minBlockUs);  // untouched
  io->send(R"({"t":"cfg","id":2,"i":"a","d":{"min_block_us":-4}})");
  core->loop();
  TEST_ASSERT_EQUAL(1500, goalA->config().lockoutMs);
  auto lines = io->lines();
  TEST_ASSERT_EQUAL(1, ofType(lines, "ack").size());
  auto nacks = ofType(lines, "nack");
  TEST_ASSERT_EQUAL(1, nacks.size());
  TEST_ASSERT_EQUAL_STRING("bad_args", nacks[0]["code"]);
}

static int sleeps, wakes, identifySeconds, reboots;

void test_module_commands() {
  sleeps = wakes = identifySeconds = reboots = 0;
  core->onSleep([] { sleeps++; });
  core->onWake([] { wakes++; });
  core->onIdentify([](uint16_t s) { identifySeconds = s; });
  core->onReboot([] { reboots++; });
  handshake();
  io->lines();
  io->send(R"({"t":"sleep","id":10})");
  io->send(R"({"t":"wake","id":11})");
  io->send(R"({"t":"identify","id":12,"d":{"s":7}})");
  io->send(R"({"t":"update","id":13})");  // no handler → nack
  core->loop();
  TEST_ASSERT_EQUAL(1, sleeps);
  TEST_ASSERT_EQUAL(1, wakes);
  TEST_ASSERT_EQUAL(7, identifySeconds);
  auto lines = io->lines();
  TEST_ASSERT_EQUAL(3, ofType(lines, "ack").size());
  TEST_ASSERT_EQUAL(1, ofType(lines, "nack").size());

  io->send(R"({"t":"reboot","id":14})");
  core->loop();  // ack goes out first, reboot happens after the flush
  auto acks = ofType(io->lines(), "ack");
  TEST_ASSERT_EQUAL(1, acks.size());
  TEST_ASSERT_EQUAL(14, acks[0]["id"].as<uint32_t>());
  TEST_ASSERT_EQUAL(1, reboots);
}

void test_bad_input() {
  handshake();
  io->lines();
  io->send(R"({"t":"evt",)");
  io->send(R"({"t":"dance","id":4})");
  io->send(R"({"id":4})");
  io->send(R"({"t":"cmd","i":"x","c":"inc"})");
  io->send("some raw text from somewhere");
  io->send("[1,2]");
  core->loop();
  auto errs = ofType(io->lines(), "err");
  TEST_ASSERT_EQUAL(4, errs.size());
  TEST_ASSERT_EQUAL_STRING("bad_json", errs[0]["code"]);
  TEST_ASSERT_EQUAL_STRING("unknown_type", errs[1]["code"]);
  TEST_ASSERT_EQUAL(4, errs[1]["ref"].as<uint32_t>());
  TEST_ASSERT_EQUAL_STRING("schema", errs[2]["code"]);
  TEST_ASSERT_EQUAL_STRING("schema", errs[3]["code"]);
}

void test_too_long_line_resyncs() {
  handshake();
  io->lines();
  io->send(std::string(3000, 'x'));
  io->send(R"({"t":"ping","id":5})");
  for (int i = 0; i < 10; i++) core->loop();  // loop() reads at most 512 bytes per call
  auto lines = io->lines();
  auto errs = ofType(lines, "err");
  TEST_ASSERT_EQUAL(1, errs.size());
  TEST_ASSERT_EQUAL_STRING("too_long", errs[0]["code"]);
  TEST_ASSERT_EQUAL(1, ofType(lines, "pong").size());
}

void test_ready_triggers_sync() {
  TEST_ASSERT_FALSE(counter->synced);
  handshake(false);
  TEST_ASSERT_TRUE(counter->synced);
  TEST_ASSERT_FALSE(core->adopted());
}

void test_status_and_log() {
  handshake();
  io->lines();
  goalA->fault("beam blocked");
  core->log(LogInfo, "level %d", 512);
  auto lines = io->lines();
  auto st = ofType(lines, "status");
  TEST_ASSERT_EQUAL(1, st.size());
  TEST_ASSERT_EQUAL_STRING("a", st[0]["i"]);
  TEST_ASSERT_EQUAL_STRING("fault", st[0]["s"]);
  auto logs = ofType(lines, "log");
  TEST_ASSERT_EQUAL_STRING("level 512", logs[0]["m"]);
}

void test_emit_at_explicit_time() {
  handshake();
  io->lines();
  io->now = 2000000;
  uint32_t edge = io->now - 1500;  // captured by an interrupt 1.5 ms ago
  goalA->goal(core->toUs64(edge));
  auto evts = ofType(io->lines(), "evt");
  TEST_ASSERT_EQUAL(1, evts.size());
  TEST_ASSERT_EQUAL_UINT64(1998500, evts[0]["ts"].as<uint64_t>());
}

void test_to_us64_across_micros_wrap() {
  io->now = 4294967000u;
  core->loop();
  uint32_t edge = io->now;     // just before the 32-bit wrap
  io->now += 1000;             // wrapped: now is small again
  uint64_t t = core->toUs64(edge);
  TEST_ASSERT_EQUAL_UINT64(4294967000ull, t);
}

void test_component_hooks() {
  TEST_ASSERT_EQUAL(1, counter->begins);  // called from Core::begin in setUp
  core->loop();
  core->loop();
  TEST_ASSERT_EQUAL(2, counter->updates);
}

void test_clock64_wraps() {
  Clock64 c;
  TEST_ASSERT_EQUAL_UINT64(4294967000ull, c.update(4294967000u));
  TEST_ASSERT_EQUAL_UINT64(4294967296ull + 100, c.update(100));
  TEST_ASSERT_EQUAL_UINT64(4294967296ull + 200, c.update(200));
}

void test_line_buffer() {
  LineBuffer<8> b;
  const char* in = "ab\r\n\nlonger-than-seven\nok\n";
  std::vector<std::string> got;
  int tooLong = 0;
  for (const char* p = in; *p; p++) {
    auto r = b.feed(*p);
    if (r == LineBuffer<8>::Line) got.push_back(std::string(b.data(), b.length()));
    if (r == LineBuffer<8>::TooLong) tooLong++;
  }
  TEST_ASSERT_EQUAL(2, got.size());
  TEST_ASSERT_EQUAL_STRING("ab", got[0].c_str());
  TEST_ASSERT_EQUAL_STRING("ok", got[1].c_str());
  TEST_ASSERT_EQUAL(1, tooLong);
}

int main(int, char**) {
  UNITY_BEGIN();
  RUN_TEST(test_announces_after_boot);
  RUN_TEST(test_hello_gets_announce);
  RUN_TEST(test_hello_incompatible_version);
  RUN_TEST(test_ping_pong);
  RUN_TEST(test_event_before_ready_is_queued);
  RUN_TEST(test_event_acked_is_not_resent);
  RUN_TEST(test_event_retry_then_give_up);
  RUN_TEST(test_orphaned_pauses_retries_and_recovers);
  RUN_TEST(test_sequence_wraps_around);
  RUN_TEST(test_queue_overflow_drops_oldest);
  RUN_TEST(test_command_dispatch_and_dedup);
  RUN_TEST(test_command_nacks_and_errors);
  RUN_TEST(test_goal_sensor_partial_config);
  RUN_TEST(test_module_commands);
  RUN_TEST(test_bad_input);
  RUN_TEST(test_too_long_line_resyncs);
  RUN_TEST(test_ready_triggers_sync);
  RUN_TEST(test_status_and_log);
  RUN_TEST(test_emit_at_explicit_time);
  RUN_TEST(test_to_us64_across_micros_wrap);
  RUN_TEST(test_component_hooks);
  RUN_TEST(test_clock64_wraps);
  RUN_TEST(test_line_buffer);
  return UNITY_END();
}
