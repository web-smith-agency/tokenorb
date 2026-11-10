// =====================================================================
//  TokenOrb – Nutzungsanzeige fuer Claude Code (5h- und Wochenlimit)
//  Hardware: ESP32-S3 / ESP32 + GC9A01 1,28" rund, 2 Tasten, WS2812
// =====================================================================
#include <Arduino.h>
#include <Adafruit_NeoPixel.h>
#include <Preferences.h>

#include "comms.h"
#include "config.h"
#include "display_cfg.hpp"
#include "state.h"
#include "ui.h"

AppState g_state;

namespace {

LGFX tft;
Adafruit_NeoPixel leds(LED_COUNT, PIN_LED, NEO_GRB + NEO_KHZ800);
Preferences prefs;

const uint8_t BRIGHT_LEVELS[] = {100, 60, 30, 10};   // Prozent
int g_brightIdx = 0;
int g_brightPct = 100;     // aktuell gewuenschte Helligkeit
int g_appliedBright = -1;  // zuletzt gesetzter Wert (0..255)
char g_bleName[20];

// ---------------------------------------------------------------------
// Tasten (gegen GND, interner Pull-up), entprellt, kurz/lang
// ---------------------------------------------------------------------
enum BtnEvent { EV_NONE, EV_SHORT, EV_LONG };

struct Button {
  uint8_t pin;
  bool raw = false, down = false, longFired = false, suppressed = false;
  uint32_t lastChange = 0, downAt = 0;

  explicit Button(uint8_t p) : pin(p) {}
  void begin() { pinMode(pin, INPUT_PULLUP); }

  BtnEvent update(uint32_t now, bool otherDown) {
    bool r = digitalRead(pin) == LOW;
    if (r != raw) { raw = r; lastChange = now; }
    BtnEvent ev = EV_NONE;
    if (now - lastChange > 25 && raw != down) {
      down = raw;
      if (down) { downAt = now; longFired = false; suppressed = false; }
      else if (!longFired && !suppressed) ev = EV_SHORT;
    }
    if (down && !longFired && !suppressed && !otherDown && now - downAt > LONG_PRESS_MS) {
      longFired = true;
      ev = EV_LONG;
    }
    return ev;
  }
};

Button btn1(PIN_BTN1), btn2(PIN_BTN2);

// ---------------------------------------------------------------------
void applyBrightness() {
  int pct = g_brightPct;
  // ohne PC nach einer Weile dimmen (spart Akku, schont Augen nachts)
  if (!g_state.demo && !g_state.linked() && millis() > IDLE_DIM_MS &&
      (g_state.lastLinkMs == 0 || millis() - g_state.lastLinkMs > IDLE_DIM_MS)) {
    pct = min(pct, 8);
  }
  if (uiAsleep()) pct = min(pct, SLEEP_BRIGHT_PCT);   // Maskottchen schlaeft

  int v = map(constrain(pct, 1, 100), 1, 100, 3, 255);
  if (v != g_appliedBright) {
    tft.setBrightness(v);
    g_appliedBright = v;
  }
}

void setBrightIdx(int idx) {
  g_brightIdx = idx % (int)sizeof(BRIGHT_LEVELS);
  g_brightPct = BRIGHT_LEVELS[g_brightIdx];
  prefs.putUChar("bri", g_brightIdx);
  char msg[24];
  snprintf(msg, sizeof(msg), "Helligkeit %d%%", g_brightPct);
  uiToast(msg);
}

void setView(int v) {
  uiSetView(v);
  prefs.putUChar("view", uiGetView());
}

// ---------------------------------------------------------------------
// Demo-Modus: laesst die Anzeige mit Fantasiewerten durchlaufen
// (praktisch zum Testen ohne PC und fuer Verkaufsvideos)
// ---------------------------------------------------------------------
void updateDemo() {
  float t = millis() / 1000.0f;
  float h5 = 50.0f + 49.0f * sinf(t / 7.0f);
  float d7 = 35.0f + 30.0f * sinf(t / 23.0f + 1.0f);
  g_state.h5.pct = h5;
  g_state.d7.pct = d7;
  // Restzeit passend zum Verbrauch, damit Tempo-Anzeige/Warnung sichtbar werden
  g_state.h5.remainS = (int32_t)(18000.0f * (0.55f + 0.4f * sinf(t / 11.0f)));
  g_state.d7.remainS = (int32_t)(604800.0f * 0.45f);
  g_state.ageS = 5;
  g_state.rxMs = millis();
  g_state.hasData = true;
}

// ---------------------------------------------------------------------
// LEDs: Farbe = Session-Auslastung, sanftes "Atmen", bei Warnung schneller
// ---------------------------------------------------------------------
void updateLeds() {
  static uint32_t last = 0;
  uint32_t now = millis();
  if (now - last < 30) return;
  last = now;

  bool alarm, idle;
  uint32_t c = uiStatusColor(alarm, idle);
  float period = alarm ? 450.0f : (idle ? 4000.0f : 2600.0f);
  float wave = 0.5f + 0.5f * sinf(now / period * TWO_PI);
  float level = idle ? (0.05f + 0.15f * wave) : (0.35f + 0.65f * wave);
  level *= LED_MAX_BRIGHT / 255.0f;
  level *= max(0.15f, g_appliedBright / 255.0f);

  uint8_t r = ((c >> 16) & 0xFF) * level;
  uint8_t g = ((c >> 8) & 0xFF) * level;
  uint8_t b = (c & 0xFF) * level;
  for (int i = 0; i < LED_COUNT; i++) leds.setPixelColor(i, leds.Color(r, g, b));
  leds.show();
}

void updateBattery() {
#if PIN_BAT_ADC >= 0
  static uint32_t last = 0;
  if (last && millis() - last < 10000) return;
  last = millis();
  uint32_t mv = 0;
  for (int i = 0; i < 8; i++) mv += analogReadMilliVolts(PIN_BAT_ADC);
  mv = mv / 8 * 2;  // Spannungsteiler 1:1
  int pct = map(constrain((int)mv, 3300, 4150), 3300, 4150, 0, 100);
  g_state.batteryPct = pct;
#endif
}

void handleButtons() {
  uint32_t now = millis();
  BtnEvent e1 = btn1.update(now, btn2.down);
  BtnEvent e2 = btn2.update(now, btn1.down);

  // jeder Tastendruck zaehlt als Aktivitaet; schlaeft das Maskottchen, weckt der erste Druck nur
  static bool prevAny = false;
  bool anyDown = btn1.down || btn2.down;
  if (anyDown && !prevAny) {
    bool asleep = uiAsleep();
    g_state.touch();
    if (asleep) {
      btn1.suppressed = btn1.down;
      btn2.suppressed = btn2.down;
      prevAny = anyDown;
      return;
    }
  }
  prevAny = anyDown;

  // beide Tasten 2 s halten -> Demo an/aus
  if (btn1.down && btn2.down && !btn1.suppressed &&
      now - max(btn1.downAt, btn2.downAt) > COMBO_PRESS_MS) {
    btn1.suppressed = btn2.suppressed = true;
    onCmdDemo(!g_state.demo);
    return;
  }

  // Taste 1: kurz = Ansicht wechseln, lang = Helligkeit
  if (e1 == EV_SHORT) setView(uiGetView() + 1);
  if (e1 == EV_LONG) setBrightIdx(g_brightIdx + 1);

  // Taste 2: wird an den PC gemeldet (Aktion in der App einstellbar)
  if (e2 != EV_NONE) {
    if (g_state.linked()) {
      commsSend(e2 == EV_SHORT ? "K 1" : "K 2");
      uiToast(e2 == EV_SHORT ? "PC-Taste" : "PC-Taste lang");
    } else {
      uiToast("kein PC verbunden");
    }
  }
}

}  // namespace

// ---- Befehle vom PC ---------------------------------------------------
void onCmdBrightness(int pct) { g_brightPct = constrain(pct, 1, 100); }
void onCmdView(int view) { setView(view); }
void onCmdDemo(bool on) {
  g_state.demo = on;
  if (!on) {
    // Demo-Werte verwerfen
    g_state.hasData = false;
    g_state.h5.pct = g_state.d7.pct = -1;
  }
  uiToast(on ? "Demo an" : "Demo aus");
}

// =====================================================================
void setup() {
  g_state.h5.lengthS = 5UL * 3600UL;
  g_state.d7.lengthS = 7UL * 86400UL;
  g_state.touch();

  prefs.begin("orb", false);
  g_brightIdx = prefs.getUChar("bri", 0) % sizeof(BRIGHT_LEVELS);
  g_brightPct = BRIGHT_LEVELS[g_brightIdx];

  uint64_t mac = ESP.getEfuseMac();
  snprintf(g_bleName, sizeof(g_bleName), "%s%02X%02X", BLE_PREFIX,
           (uint8_t)(mac >> 32), (uint8_t)(mac >> 40));

  btn1.begin();
  btn2.begin();
  leds.begin();
  leds.clear();
  leds.show();

  tft.init();
  tft.setRotation(TFT_ROTATION);
  tft.fillScreen(TFT_BLACK);
  applyBrightness();

  uiBegin(&tft);
  uiSetView(prefs.getUChar("view", 0));
  uiSplash();

  commsBegin(g_bleName);

#ifdef SIM_BUILD
  Serial.println("TokenOrb Simulation bereit. Testbeispiel:  U 42 5400 63 200000 10");
#endif
}

void loop() {
  static uint32_t lastFrame = 0;
  commsLoop();
  handleButtons();
  if (g_state.demo) updateDemo();
  updateBattery();
  applyBrightness();
  updateLeds();

  uint32_t now = millis();
  if (now - lastFrame >= 33) {   // ~30 fps
    lastFrame = now;
    uiRender();
  }
  delay(1);
}
