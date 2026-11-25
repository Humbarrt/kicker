// Arduino glue for the protocol core. Not compiled in the native test build.
#ifdef ARDUINO

#include "KickerModule.h"

#include <string.h>

#if defined(ARDUINO_ARCH_RP2040)
#include <pico/unique_id.h>
#define KM_UPDATE_METHOD "uf2"
#elif defined(ESP32)
#define KM_UPDATE_METHOD "esptool"
#else
#define KM_UPDATE_METHOD "none"
#endif

KickerModule* KickerModule::instance_ = nullptr;

static kicker::ModuleInfo makeInfo(const char* manifestId, const char* fwName, const char* fwVersion,
                                   const char* hardware) {
  kicker::ModuleInfo info;
  info.manifestId = manifestId;
  info.fwName = fwName;
  info.fwVersion = fwVersion;
  info.hardware = hardware;
  info.update = KM_UPDATE_METHOD;
  return info;
}

KickerModule::KickerModule(const char* manifestId, const char* fwVersion, const char* hardware)
    : core_(io_, makeInfo(manifestId, fwName_, fwVersion, hardware)) {
  // firmware name = manifest id with '/' → '-', e.g. "kicker/goal-sensor" → "kicker-goal-sensor"
  strncpy(fwName_, manifestId, sizeof(fwName_) - 1);
  fwName_[sizeof(fwName_) - 1] = '\0';
  for (char* p = fwName_; *p; p++) {
    if (*p == '/') *p = '-';
  }
  instance_ = this;
}

#if defined(ARDUINO_ARCH_RP2040)
static void rebootBoard() { rp2040.reboot(); }
static void enterBootloader() { rp2040.rebootToBootloader(); }
#elif defined(ESP32)
static void rebootBoard() { ESP.restart(); }
#endif

void KickerModule::begin(Stream& stream) {
  io_.stream = &stream;

  if (core_.uid()[0] == '\0') {
#if defined(ARDUINO_ARCH_RP2040)
    char id[2 * PICO_UNIQUE_BOARD_ID_SIZE_BYTES + 1];
    pico_get_unique_board_id_string(id, sizeof(id));
    core_.setUid(id);
#elif defined(ESP32)
    char id[17];
    snprintf(id, sizeof(id), "%016llx", (unsigned long long)ESP.getEfuseMac());
    core_.setUid(id);
#else
    // TODO(M7): generate once and store in flash (spec 01 §5). Until then: not stable across boots.
    char id[17];
    snprintf(id, sizeof(id), "%08lx%08lx", (unsigned long)random(0x7fffffff), (unsigned long)micros());
    core_.setUid(id);
#endif
  }

#ifdef LED_BUILTIN
  pinMode(LED_BUILTIN, OUTPUT);
  core_.onIdentify(defaultIdentify);
#endif
#if defined(ARDUINO_ARCH_RP2040) || defined(ESP32)
  core_.onReboot(rebootBoard);
#endif
#if defined(ARDUINO_ARCH_RP2040)
  core_.onUpdate(enterBootloader);
#endif

  core_.begin();
}

void KickerModule::loop() {
  core_.loop();
  updateIdentifyLed();
}

void KickerModule::defaultIdentify(uint16_t seconds) {
  if (!instance_) return;
  instance_->blinkUntilMs_ = millis() + uint32_t(seconds) * 1000;
  instance_->blinking_ = true;
}

void KickerModule::updateIdentifyLed() {
#ifdef LED_BUILTIN
  if (!blinking_) return;
  uint32_t now = millis();
  if (int32_t(now - blinkUntilMs_) >= 0) {
    blinking_ = false;
    digitalWrite(LED_BUILTIN, LOW);
    return;
  }
  digitalWrite(LED_BUILTIN, (now / 150) % 2 ? HIGH : LOW);
#endif
}

#endif  // ARDUINO
