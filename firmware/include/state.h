#pragma once
#include <Arduino.h>

// Ein Nutzungsfenster (5 Stunden oder 7 Tage)
struct Window {
  float pct = -1;          // 0..100, -1 = unbekannt
  int32_t remainS = -1;    // Sekunden bis Reset zum Empfangszeitpunkt, -1 = unbekannt
  uint32_t lengthS = 0;    // Fensterlaenge in Sekunden

  // aktuelle Werte, hochgerechnet seit Empfang
  float pctNow(uint32_t sinceS) const {
    if (pct < 0) return -1;
    if (remainS >= 0 && (int32_t)sinceS >= remainS) return 0;  // Fenster ist zurueckgesetzt
    return pct;
  }
  int32_t remainNow(uint32_t sinceS) const {
    if (remainS < 0) return -1;
    int32_t r = remainS - (int32_t)sinceS;
    return r > 0 ? r : 0;
  }
  // Anteil des Fensters, der schon vergangen ist (0..1), -1 = unbekannt
  float elapsedFrac(uint32_t sinceS) const {
    int32_t r = remainNow(sinceS);
    if (r < 0 || lengthS == 0) return -1;
    float f = 1.0f - (float)r / (float)lengthS;
    return constrain(f, 0.0f, 1.0f);
  }
  // Prognose: Sekunden bis 100 % beim aktuellen Tempo (-1 = kein Problem/unbekannt)
  int32_t secondsToLimit(uint32_t sinceS) const {
    float p = pctNow(sinceS);
    int32_t r = remainNow(sinceS);
    if (p < 25 || r <= 0 || lengthS == 0) return -1;
    float elapsed = (float)lengthS - (float)r;
    // erst nach 15 % des Fensters hochrechnen, vorher ist die Prognose zu wackelig
    if (elapsed < lengthS * 0.15f) return -1;
    float rate = p / elapsed;               // Prozent pro Sekunde
    float toLimit = (100.0f - p) / rate;
    if (toLimit >= r) return -1;            // reicht bis zum Reset
    return (int32_t)toLimit;
  }
};

enum class Transport : uint8_t { None, Usb, Ble };

struct AppState {
  Window h5;               // 5-Stunden-Fenster
  Window d7;               // 7-Tage-Fenster
  bool hasData = false;    // je ein Datenpaket empfangen
  uint32_t ageS = 0;       // wie alt die Daten beim Senden waren (letztes Claude-Code-Update)
  uint32_t rxMs = 0;       // millis() beim Empfang des letzten Datenpakets
  uint32_t lastLinkMs = 0; // millis() der letzten Nachricht vom PC (inkl. Ping)
  Transport transport = Transport::None;
  bool demo = false;
  int otaPct = -1;         // Firmware-Update ueber USB: 0..100, -1 = kein Update
  int batteryPct = -1;     // -1 = kein Akku gemessen
  uint32_t activityMs = 0; // millis() der letzten "echten" Aenderung (neue Werte, Tastendruck)

  uint32_t sinceS() const { return (millis() - rxMs) / 1000; }
  uint32_t idleMs() const { return millis() - activityMs; }
  void touch() { activityMs = millis(); }
  bool linked() const {
    return lastLinkMs != 0 && (millis() - lastLinkMs) < 45000UL;
  }
};

extern AppState g_state;
