#include "ui.h"
#include "config.h"
#include "state.h"
#if SHOW_BOOT_LOGO
#include "logo.h"
#endif

// =====================================================================
//  Oberflaeche: 240x240, rund. Alles wird in einen Sprite gezeichnet und
//  dann in einem Rutsch uebertragen -> kein Flackern.
// =====================================================================

namespace {

lgfx::LGFX_Device* g_tft = nullptr;
LGFX_Sprite* g_cv = nullptr;   // Leinwand
int g_offY = 0;                // ILI9341 (Simulation): 240x240 mittig auf 240x320

int g_view = VIEW_GAUGE;
float g_dispH5 = 0, g_dispD7 = 0;   // geglaettete Anzeige-Werte
char g_toast[32] = {0};
uint32_t g_toastUntil = 0;
uint32_t g_nextBlink = 0, g_blinkStart = 0;

constexpr int CX = 120, CY = 120;

// ---- Farben (0xRRGGBB) ------------------------------------------------
constexpr uint32_t C_BG     = 0x000000u;
constexpr uint32_t C_TRACK  = 0x1C2026u;
constexpr uint32_t C_TRACK2 = 0x14171Bu;
constexpr uint32_t C_TEXT   = 0xF5F7FAu;
constexpr uint32_t C_DIM    = 0x8A93A0u;
constexpr uint32_t C_FAINT  = 0x4A515Cu;
constexpr uint32_t C_GREEN  = 0x34D399u;
constexpr uint32_t C_AMBER  = 0xFBBF24u;
constexpr uint32_t C_RED    = 0xF87171u;
constexpr uint32_t C_SLEEP  = 0x6B7FA8u;
constexpr uint32_t C_PANEL  = 0x1A1E24u;

uint32_t mix(uint32_t a, uint32_t b, float t) {
  t = constrain(t, 0.0f, 1.0f);
  uint8_t r = ((a >> 16) & 0xFF) + (((int)((b >> 16) & 0xFF) - (int)((a >> 16) & 0xFF)) * t);
  uint8_t g = ((a >> 8) & 0xFF) + (((int)((b >> 8) & 0xFF) - (int)((a >> 8) & 0xFF)) * t);
  uint8_t bl = (a & 0xFF) + (((int)(b & 0xFF) - (int)(a & 0xFF)) * t);
  return ((uint32_t)r << 16) | ((uint32_t)g << 8) | bl;
}

uint32_t usageColor(float pct) {
  if (pct < 0) return C_FAINT;
  if (pct < 50) return C_GREEN;
  if (pct < 80) return mix(C_GREEN, C_AMBER, (pct - 50) / 30.0f);
  if (pct < 95) return mix(C_AMBER, C_RED, (pct - 80) / 15.0f);
  return C_RED;
}

// ---- Zeitformat -------------------------------------------------------
void fmtDur(int32_t s, char* out, size_t n) {
  if (s < 0) { snprintf(out, n, "--"); return; }
  if (s >= 86400) snprintf(out, n, "%ldT %ldh", (long)(s / 86400), (long)((s % 86400) / 3600));
  else if (s >= 3600) snprintf(out, n, "%ldh %02ldm", (long)(s / 3600), (long)((s % 3600) / 60));
  else if (s >= 60) snprintf(out, n, "%ldm", (long)(s / 60));
  else snprintf(out, n, "<1m");
}

// ---- Bogen-Helfer -----------------------------------------------------
// Skala: 270 Grad, Start unten links (135 Grad), im Uhrzeigersinn
constexpr float GAUGE_START = 135.0f;
constexpr float GAUGE_SWEEP = 270.0f;

void arcRaw(int rIn, int rOut, float a0, float a1, uint32_t col) {
  if (a1 <= a0) return;
  // in Stuecken <= 360 zeichnen
  if (a1 > 360.0f && a0 < 360.0f) {
    g_cv->fillArc(CX, CY, rOut, rIn, a0, 360.0f, col);
    g_cv->fillArc(CX, CY, rOut, rIn, 0.0f, a1 - 360.0f, col);
  } else if (a0 >= 360.0f) {
    g_cv->fillArc(CX, CY, rOut, rIn, a0 - 360.0f, a1 - 360.0f, col);
  } else {
    g_cv->fillArc(CX, CY, rOut, rIn, a0, a1, col);
  }
}

void capAt(int rIn, int rOut, float angDeg, uint32_t col) {
  float rad = angDeg * DEG_TO_RAD;
  float rm = (rIn + rOut) * 0.5f;
  g_cv->fillCircle(CX + cosf(rad) * rm, CY + sinf(rad) * rm, (rOut - rIn) * 0.5f, col);
}

// Bogen von f0 bis f1 (0..1 der Skala) mit runden Enden
void gaugeArc(int rIn, int rOut, float f0, float f1, uint32_t col) {
  f0 = constrain(f0, 0.0f, 1.0f);
  f1 = constrain(f1, 0.0f, 1.0f);
  if (f1 <= f0) return;
  float a0 = GAUGE_START + GAUGE_SWEEP * f0;
  float a1 = GAUGE_START + GAUGE_SWEEP * f1;
  arcRaw(rIn, rOut, a0, a1, col);
  capAt(rIn, rOut, a0, col);
  capAt(rIn, rOut, a1, col);
}

void paceTick(int rIn, int rOut, float frac) {
  if (frac < 0) return;
  float rad = (GAUGE_START + GAUGE_SWEEP * frac) * DEG_TO_RAD;
  float c = cosf(rad), s = sinf(rad);
  g_cv->drawWideLine(CX + c * (rIn - 3), CY + s * (rIn - 3), CX + c * (rOut + 3), CY + s * (rOut + 3),
                     2.2f, C_TEXT);
}

// Beide Ringe: aussen 5h (dick), innen 7d (duenn)
void drawRings(float h5, float d7, float pace5, float pace7, bool thin = false) {
  const int o1 = thin ? 117 : 117, i1 = thin ? 111 : 104;
  const int o2 = thin ? 107 : 98, i2 = thin ? 103 : 91;
  gaugeArc(i1, o1, 0, 1, C_TRACK);
  gaugeArc(i2, o2, 0, 1, C_TRACK2);
  if (h5 > 0.3f) gaugeArc(i1, o1, 0, h5 / 100.0f, usageColor(h5));
  if (d7 > 0.3f) gaugeArc(i2, o2, 0, d7 / 100.0f, mix(usageColor(d7), C_TEXT, 0.15f));
  paceTick(i1, o1, pace5);
  paceTick(i2, o2, pace7);
}

// ---- Text-Helfer ------------------------------------------------------
void text(const char* s, int x, int y, const lgfx::IFont* f, uint32_t col,
          textdatum_t datum = middle_center) {
  g_cv->setFont(f);
  g_cv->setTextDatum(datum);
  g_cv->setTextColor(col);
  g_cv->drawString(s, x, y);
}

// grosse Prozentzahl mit kleinem %-Zeichen, horizontal zentriert
void bigPercent(float pct, int baselineY, uint32_t col) {
  char num[8];
  if (pct < 0) snprintf(num, sizeof(num), "--");
  else snprintf(num, sizeof(num), "%d", (int)roundf(pct));
  g_cv->setFont(&fonts::DejaVu56);
  int w1 = g_cv->textWidth(num);
  g_cv->setFont(&fonts::DejaVu24);
  int w2 = pct < 0 ? 0 : g_cv->textWidth("%");
  int x = CX - (w1 + w2 + 2) / 2;
  text(num, x, baselineY, &fonts::DejaVu56, col, baseline_left);
  if (pct >= 0) text("%", x + w1 + 2, baselineY, &fonts::DejaVu24, C_DIM, baseline_left);
}

// ---- Status-Leiste unten (im offenen Teil der Skala) ------------------
void drawStatusBar() {
  const int y = 214;
  char buf[24];
  const char* link = "";
  uint32_t col = C_FAINT;
  if (g_state.demo) { link = "DEMO"; col = C_AMBER; }
  else if (!g_state.linked()) { link = "OFFLINE"; }
  else if (g_state.transport == Transport::Ble) { link = "BT"; col = C_DIM; }
  else { link = "USB"; col = C_DIM; }

  if (g_state.batteryPct >= 0) {
    snprintf(buf, sizeof(buf), "%s  %d%%", link, g_state.batteryPct);
    text(buf, CX, y, &fonts::DejaVu9, col);
  } else {
    text(link, CX, y, &fonts::DejaVu9, col);
  }
  // Punkt: Daten frisch (gruen) / alt (grau)
  if (g_state.hasData && g_state.linked() && !g_state.demo) {
    uint32_t age = g_state.ageS + g_state.sinceS();
    g_cv->fillCircle(CX, y - 12, 2, age < 900 ? C_GREEN : C_FAINT);
  }
}

// ---- Maskottchen "Orb" --------------------------------------------------
enum Mood { MOOD_CALM, MOOD_BUSY, MOOD_PANIC, MOOD_SLEEP };

Mood moodFor(float pct, bool asleep) {
  if (asleep) return MOOD_SLEEP;
  if (pct >= 85) return MOOD_PANIC;
  if (pct >= 60) return MOOD_BUSY;
  return MOOD_CALM;
}

float blinkAmount(uint32_t now) {
  if (now >= g_nextBlink) {
    g_blinkStart = now;
    g_nextBlink = now + 2500 + (esp_random() % 3500);
  }
  uint32_t d = now - g_blinkStart;
  if (d < 70) return d / 70.0f;
  if (d < 140) return 1.0f - (d - 70) / 70.0f;
  return 0;
}

// Zusatz-Ausdruck fuer Leerlauf-Animationen
struct Expr {
  float droop = 0;      // 0..1 Lider haengen (muede)
  float yawn = 0;       // 0..1 Gaehnen
  float look = NAN;     // -1..1 Blick nach links/rechts erzwingen
  bool whistle = false; // pfeifen (kleiner Mund + Noten)
  bool surprised = false;
};

void note(int x, int y, uint32_t col) {
  g_cv->fillEllipse(x, y, 4, 3, col);
  g_cv->fillRect(x + 3, y - 11, 2, 11, col);
  g_cv->fillTriangle(x + 4, y - 11, x + 10, y - 7, x + 4, y - 6, col);
}

void drawBuddy(int cx, int cy, int r, Mood mood, float pct, uint32_t now, const Expr& x = Expr()) {
  uint32_t body = (mood == MOOD_SLEEP) ? C_SLEEP : usageColor(pct < 0 ? 0 : pct);
  // Schatten + Koerper + Glanz
  g_cv->fillEllipse(cx, cy + r + 8, r * 0.7f, 5, 0x0C0E11u);
  g_cv->fillCircle(cx, cy, r, mix(body, 0x000000u, 0.25f));
  g_cv->fillCircle(cx - 2, cy - 3, r - 3, body);
  g_cv->fillCircle(cx - r * 0.42f, cy - r * 0.45f, r * 0.14f, mix(body, 0xFFFFFFu, 0.55f));

  const uint32_t ink = 0x14171Bu;
  int ex = r * 0.36f, ey = cy - r * 0.10f;
  int ew = r * 0.17f, eh = r * 0.24f;

  if (mood == MOOD_SLEEP) {
    g_cv->fillRoundRect(cx - ex - ew, ey, ew * 2, 4, 2, ink);
    g_cv->fillRoundRect(cx + ex - ew, ey, ew * 2, 4, 2, ink);
    // ruhiges Atmen: Mund geht leicht auf und zu
    float br = 0.5f + 0.5f * sinf(now / 1400.0f);
    g_cv->fillEllipse(cx, cy + r * 0.36f, 4 + br * 2, 2 + br * 2, ink);
    // "z" steigen auf
    for (int i = 0; i < 3; i++) {
      float ph = fmodf(now / 2200.0f + i / 3.0f, 1.0f);
      int zx = cx + r * 0.65f + ph * 22 + sinf(ph * 6.0f) * 3;
      int zy = cy - r * 0.55f - ph * 40;
      const lgfx::IFont* f = (i == 0) ? &fonts::DejaVu18 : (i == 1 ? &fonts::DejaVu12 : &fonts::DejaVu9);
      text("z", zx, zy, f, mix(C_SLEEP, C_BG, ph));
    }
    return;
  }

  float blink = x.yawn > 0.2f ? 0 : blinkAmount(now);
  if (mood == MOOD_PANIC || x.surprised) { ew *= 1.15f; eh *= 1.15f; }
  if (mood == MOOD_BUSY) eh *= 0.8f;
  float squeeze = 1.0f - 0.75f * x.yawn;          // beim Gaehnen kneifen die Augen zu
  int h = max(3, (int)(eh * (1.0f - blink) * squeeze));
  // Augen
  g_cv->fillEllipse(cx - ex, ey, ew, h, 0xFFFFFFu);
  g_cv->fillEllipse(cx + ex, ey, ew, h, 0xFFFFFFu);
  if (blink < 0.6f && x.yawn < 0.6f) {
    float lx = isnan(x.look) ? sinf(now / 1300.0f) * ew * 0.35f : x.look * ew * 0.55f;
    float ly = isnan(x.look) ? cosf(now / 1700.0f) * eh * 0.2f : 0;
    if (x.droop > 0) ly += eh * 0.25f * x.droop;   // muede: Blick nach unten
    int pr = (mood == MOOD_PANIC || x.surprised) ? ew * 0.38f : ew * 0.55f;
    g_cv->fillCircle(cx - ex + lx, ey + ly, pr, ink);
    g_cv->fillCircle(cx + ex + lx, ey + ly, pr, ink);
  }
  float lid = (mood == MOOD_BUSY) ? 0.55f : 0;
  lid = max(lid, x.droop * 0.7f);
  if (lid > 0.02f) {
    g_cv->fillRect(cx - ex - ew - 1, ey - h - 1, ew * 2 + 2, h * 2 * lid + 1, body);
    g_cv->fillRect(cx + ex - ew - 1, ey - h - 1, ew * 2 + 2, h * 2 * lid + 1, body);
  }

  // Mund
  int my = cy + r * 0.38f;
  if (x.yawn > 0.05f) {
    g_cv->fillEllipse(cx, my + 2, 6 + 5 * x.yawn, 3 + 11 * x.yawn, ink);
    g_cv->fillEllipse(cx, my + 4 + 6 * x.yawn, 3 + 3 * x.yawn, 1 + 3 * x.yawn, 0xE11D48u);
    return;
  }
  if (x.whistle) {
    g_cv->fillCircle(cx + 3, my + 1, 4, ink);
    for (int i = 0; i < 2; i++) {
      float ph = fmodf(now / 1300.0f + i * 0.5f, 1.0f);
      note(cx + r * 0.55f + ph * 20, cy - r * 0.15f - ph * 30, mix(C_TEXT, C_BG, ph));
    }
    return;
  }
  if (x.surprised) {
    g_cv->fillEllipse(cx, my + 2, 5, 6, ink);
    text("!", cx + r * 0.85f, cy - r * 0.75f, &fonts::DejaVu24, C_AMBER);
    return;
  }
  switch (mood) {
    case MOOD_CALM:
      if (x.droop > 0.3f) g_cv->fillRoundRect(cx - 7, my, 14, 3, 1, ink);
      else g_cv->fillArc(cx, my - 6, 11, 8, 20, 160, ink);
      break;
    case MOOD_BUSY:
      g_cv->fillRoundRect(cx - 9, my, 18, 3, 1, ink);
      break;
    case MOOD_PANIC: {
      g_cv->fillEllipse(cx, my + 2, 7, 9, ink);
      // Schweisstropfen
      int sx = cx + r * 0.78f, sy = cy - r * 0.55f + (int)(fmodf(now / 900.0f, 1.0f) * 10);
      g_cv->fillTriangle(sx, sy - 9, sx - 5, sy, sx + 5, sy, 0x93C5FDu);
      g_cv->fillCircle(sx, sy + 1, 5, 0x93C5FDu);
      break;
    }
    default:
      break;
  }
}

// ---- Leerlauf: gelangweilt -> muede -> schlafen ----------------------------
enum IdleStage { IDLE_ACTIVE = 0, IDLE_BORED, IDLE_TIRED, IDLE_ASLEEP };
enum IdleAnim { ANIM_NONE, ANIM_LOOK, ANIM_WHISTLE, ANIM_HOP, ANIM_YAWN };
IdleAnim g_anim = ANIM_NONE;
uint32_t g_animStart = 0, g_animNext = 0, g_wakeAt = 0;
int g_prevStage = IDLE_ACTIVE;
constexpr uint32_t ANIM_LEN = 3200;

int idleStage() {
  if (g_state.demo) return IDLE_ACTIVE;
  uint32_t i = g_state.idleMs();
  if (i >= IDLE_SLEEP_MS) return IDLE_ASLEEP;
  if (i >= IDLE_TIRED_MS) return IDLE_TIRED;
  if (i >= IDLE_BORED_MS) return IDLE_BORED;
  return IDLE_ACTIVE;
}

void updateIdle(int stage, uint32_t now) {
  if (g_prevStage == IDLE_ASLEEP && stage < IDLE_ASLEEP) g_wakeAt = now;   // aufgewacht
  g_prevStage = stage;
  if (stage == IDLE_ACTIVE || stage == IDLE_ASLEEP) { g_anim = ANIM_NONE; return; }
  if (g_anim != ANIM_NONE && now - g_animStart > ANIM_LEN) g_anim = ANIM_NONE;
  if (g_anim == ANIM_NONE && (int32_t)(now - g_animNext) >= 0) {
    uint32_t r = esp_random() % 100;
    static int lastStage = -1;
    if (stage == IDLE_TIRED && lastStage != IDLE_TIRED) r = 0;   // muede werden -> erstmal gaehnen
    lastStage = stage;
    if (stage == IDLE_TIRED) g_anim = r < 65 ? ANIM_YAWN : ANIM_LOOK;
    else g_anim = r < 35 ? ANIM_LOOK : (r < 70 ? ANIM_WHISTLE : (r < 90 ? ANIM_HOP : ANIM_YAWN));
    g_animStart = now;
    g_animNext = now + ANIM_LEN + 5000 + esp_random() % 9000;
  }
}

// Ausdruck + Huepfer fuer die aktuelle Leerlauf-Phase
Expr idleExpr(int stage, uint32_t now, float& hop) {
  Expr e;
  hop = 0;
  if (g_wakeAt && now - g_wakeAt < 1400) { e.surprised = true; hop = -6 * sinf((now - g_wakeAt) / 1400.0f * PI); return e; }
  if (stage == IDLE_TIRED) e.droop = 0.55f + 0.15f * sinf(now / 2500.0f);
  float t = (now - g_animStart) / (float)ANIM_LEN;    // 0..1
  float env = sinf(constrain(t, 0.0f, 1.0f) * PI);    // weich rein und raus
  switch (g_anim) {
    case ANIM_LOOK: e.look = (t < 0.5f ? -1.0f : 1.0f) * min(1.0f, env * 2.5f); break;
    case ANIM_WHISTLE: e.whistle = true; e.look = 0.4f; break;
    case ANIM_HOP: hop = -fabsf(sinf(t * PI * 3)) * 12 * env; break;
    case ANIM_YAWN: e.yawn = env; e.droop = max(e.droop, env * 0.3f); break;
    default: break;
  }
  return e;
}

// ---- Ansichten ----------------------------------------------------------
struct Snapshot {
  float h5, d7;            // aktuelle Prozent
  int32_t r5, r7;          // Sekunden bis Reset
  float pace5, pace7;      // vergangener Anteil des Fensters
  int32_t lim5, lim7;      // Prognose bis Limit
};

Snapshot snap() {
  Snapshot s;
  uint32_t since = g_state.sinceS();
  s.h5 = g_state.h5.pctNow(since);
  s.d7 = g_state.d7.pctNow(since);
  s.r5 = g_state.h5.remainNow(since);
  s.r7 = g_state.d7.remainNow(since);
  s.pace5 = g_state.h5.elapsedFrac(since);
  s.pace7 = g_state.d7.elapsedFrac(since);
  s.lim5 = g_state.h5.secondsToLimit(since);
  s.lim7 = g_state.d7.secondsToLimit(since);
  return s;
}

void viewWaiting(uint32_t now) {
  drawRings(0, 0, -1, -1);
  float bob = sinf(now / 900.0f) * 3;
  drawBuddy(CX, 105 + bob, 38, MOOD_SLEEP, 0, now);
  text("Warte auf PC", CX, 172, &fonts::DejaVu18, C_DIM);
  text(g_state.linked() ? "keine Limit-Daten" : "App gestartet?", CX, 194, &fonts::DejaVu12, C_FAINT);
}

void viewGauge(const Snapshot& s, uint32_t now) {
  drawRings(g_dispH5, g_dispD7, s.pace5, s.pace7);

  text("SESSION", CX, 62, &fonts::DejaVu12, C_DIM);
  bigPercent(s.h5 < 0 ? -1 : g_dispH5, 128, C_TEXT);

  char buf[32], d[16];
  if (s.lim5 >= 0) {
    fmtDur(s.lim5, d, sizeof(d));
    snprintf(buf, sizeof(buf), "Limit in ~%s", d);
    bool flash = (now / 500) % 2;
    text(buf, CX, 152, &fonts::DejaVu12, flash ? C_RED : C_AMBER);
  } else {
    fmtDur(s.r5, d, sizeof(d));
    snprintf(buf, sizeof(buf), "Reset %s", d);
    text(buf, CX, 152, &fonts::DejaVu12, C_DIM);
  }

  // Woche
  if (s.d7 >= 0) {
    snprintf(buf, sizeof(buf), "Woche %d%%", (int)roundf(s.d7));
  } else {
    snprintf(buf, sizeof(buf), "Woche --");
  }
  g_cv->setFont(&fonts::DejaVu12);
  int w = g_cv->textWidth(buf);
  g_cv->fillCircle(CX - w / 2 - 8, 178, 3, usageColor(s.d7));
  text(buf, CX + 4, 178, &fonts::DejaVu12, C_TEXT);
}

void viewBuddy(const Snapshot& s, uint32_t now, int stage) {
  drawRings(g_dispH5, g_dispD7, s.pace5, s.pace7);
  Mood m = moodFor(s.h5, false);
  float speed = (m == MOOD_PANIC) ? 260.0f : (m == MOOD_BUSY ? 500.0f : 900.0f);
  if (stage == IDLE_TIRED) speed = 1600.0f;
  float bob = sinf(now / speed) * (m == MOOD_PANIC ? 2.5f : 3.5f);
  float hop;
  Expr e = idleExpr(stage, now, hop);
  drawBuddy(CX, 104 + bob + hop, 44, m, s.h5, now, e);

  char buf[32], d[16];
  fmtDur(s.r5, d, sizeof(d));
  if (s.h5 >= 0) snprintf(buf, sizeof(buf), "%d%%  -  %s", (int)roundf(s.h5), d);
  else snprintf(buf, sizeof(buf), "--");
  text(buf, CX, 182, &fonts::DejaVu18, C_TEXT);
}

void detailBlock(const char* title, float pct, int32_t remain, int32_t lim, float pace, int y) {
  char buf[40], d[16];
  text(title, CX, y, &fonts::DejaVu12, C_DIM);
  fmtDur(remain, d, sizeof(d));
  if (pct >= 0) snprintf(buf, sizeof(buf), "%d%%  -  %s", (int)roundf(pct), d);
  else snprintf(buf, sizeof(buf), "keine Daten");
  text(buf, CX, y + 22, &fonts::DejaVu18, usageColor(pct) == C_FAINT ? C_DIM : C_TEXT);

  // Tempo-Bewertung: Verbrauch vs. vergangene Zeit im Fenster
  uint32_t col = C_GREEN;
  if (lim >= 0) {
    fmtDur(lim, d, sizeof(d));
    snprintf(buf, sizeof(buf), "Limit in ~%s", d);
    col = C_RED;
  } else if (pct >= 0 && pace >= 0) {
    float diff = pct - pace * 100.0f;
    if (diff > 10) { snprintf(buf, sizeof(buf), "Tempo: zuegig"); col = C_AMBER; }
    else if (diff < -10) { snprintf(buf, sizeof(buf), "Tempo: entspannt"); }
    else { snprintf(buf, sizeof(buf), "Tempo: im Plan"); }
  } else {
    buf[0] = 0;
  }
  if (buf[0]) text(buf, CX, y + 44, &fonts::DejaVu12, col);
}

void viewDetails(const Snapshot& s) {
  drawRings(g_dispH5, g_dispD7, s.pace5, s.pace7, true);
  detailBlock("SESSION  -  Reset in", s.h5, s.r5, s.lim5, s.pace5, 50);
  detailBlock("WOCHE  -  Reset in", s.d7, s.r7, s.lim7, s.pace7, 118);

  char buf[32], d[16];
  uint32_t age = g_state.ageS + g_state.sinceS();
  fmtDur(age, d, sizeof(d));
  snprintf(buf, sizeof(buf), "Stand: vor %s", d);
  text(buf, CX, 188, &fonts::DejaVu9, C_FAINT);
}

// Schlaf-Bildschirm: nach langer Ruhe in jeder Ansicht
void viewSleep(const Snapshot& s, uint32_t now) {
  drawRings(g_dispH5, g_dispD7, -1, -1, true);
  float bob = sinf(now / 1800.0f) * 2.5f;
  drawBuddy(CX, 106 + bob, 42, MOOD_SLEEP, 0, now);
  char buf[32];
  if (s.h5 >= 0 && s.d7 >= 0) snprintf(buf, sizeof(buf), "%d%%  -  Woche %d%%", (int)roundf(s.h5), (int)roundf(s.d7));
  else if (s.h5 >= 0) snprintf(buf, sizeof(buf), "%d%%", (int)roundf(s.h5));
  else buf[0] = 0;
  if (buf[0]) text(buf, CX, 176, &fonts::DejaVu12, C_DIM);
  text("Taste = aufwecken", CX, 194, &fonts::DejaVu9, C_FAINT);
}

void drawToast(uint32_t now) {
  if (!g_toast[0] || now > g_toastUntil) return;
  g_cv->setFont(&fonts::DejaVu12);
  int w = g_cv->textWidth(g_toast) + 24;
  g_cv->fillRoundRect(CX - w / 2, 186, w, 24, 12, C_PANEL);
  g_cv->drawRoundRect(CX - w / 2, 186, w, 24, 12, C_FAINT);
  text(g_toast, CX, 198, &fonts::DejaVu12, C_TEXT);
}

}  // namespace

// =====================================================================

void uiBegin(lgfx::LGFX_Device* tft) {
  g_tft = tft;
#ifdef PANEL_ILI9341
  g_offY = 40;
#endif
  g_cv = new LGFX_Sprite(tft);
  g_cv->setColorDepth(16);
#ifdef BOARD_HAS_PSRAM
  g_cv->setPsram(true);
#endif
  if (!g_cv->createSprite(240, 240)) {
    // wenig RAM (z.B. WROOM ohne PSRAM): 8 Bit Farbtiefe
    g_cv->setPsram(false);
    g_cv->setColorDepth(8);
    g_cv->createSprite(240, 240);
  }
}

#if SHOW_BOOT_LOGO
// Logo mit Helligkeit f (0..1) mittig zeichnen
static void drawLogo(float f, int cy) {
  int x0 = CX - LOGO_W / 2, y0 = cy - LOGO_H / 2;
  for (int y = 0; y < LOGO_H; y++) {
    for (int x = 0; x < LOGO_W; x++) {
      uint16_t c = LOGO_RGB565[y * LOGO_W + x];
      if (!c) continue;
      uint8_t r = ((c >> 11) & 0x1F) * 255 / 31, g = ((c >> 5) & 0x3F) * 255 / 63, b = (c & 0x1F) * 255 / 31;
      g_cv->drawPixel(x0 + x, y0 + y, g_cv->color888(r * f, g * f, b * f));
    }
  }
}

static void logoSplash() {
  const int steps_in = 14, hold = 40, steps_out = 10;
  for (int i = 0; i < steps_in + hold + steps_out; i++) {
    float f = i < steps_in ? (i + 1) / (float)steps_in
            : (i < steps_in + hold ? 1.0f : 1.0f - (i - steps_in - hold + 1) / (float)steps_out);
    g_cv->fillScreen(C_BG);
    drawLogo(f, 112);
    if (g_tft) g_cv->pushSprite(0, g_offY);
    delay(25);
  }
}
#endif

void uiSplash() {
#if SHOW_BOOT_LOGO
  logoSplash();
#endif
  for (int i = 0; i <= 30; i++) {
    uint32_t now = millis();
    g_cv->fillScreen(C_BG);
    float f = i / 30.0f;
    gaugeArc(104, 117, 0, f, mix(C_GREEN, C_AMBER, f));
    gaugeArc(91, 98, 0, f * 0.6f, C_FAINT);
    drawBuddy(CX, 100, 34, i < 18 ? MOOD_SLEEP : MOOD_CALM, 10, now);
    text(PRODUCT_NAME, CX, 160, &fonts::DejaVu18, C_TEXT);
    text("v" FW_VERSION, CX, 182, &fonts::DejaVu9, C_FAINT);
    if (g_tft) g_cv->pushSprite(0, g_offY);
    delay(25);
  }
  delay(500);
}

void uiRender() {
  uint32_t now = millis();
  Snapshot s = snap();

  // Werte weich animieren
  float t5 = s.h5 < 0 ? 0 : s.h5;
  float t7 = s.d7 < 0 ? 0 : s.d7;
  g_dispH5 += (t5 - g_dispH5) * 0.12f;
  g_dispD7 += (t7 - g_dispD7) * 0.12f;
  if (fabsf(t5 - g_dispH5) < 0.05f) g_dispH5 = t5;
  if (fabsf(t7 - g_dispD7) < 0.05f) g_dispD7 = t7;

  g_cv->fillScreen(C_BG);
  if (g_state.otaPct >= 0) {   // Firmware-Update laeuft
    gaugeArc(100, 110, 0, 1, C_TRACK);
    gaugeArc(100, 110, 0, g_state.otaPct / 100.0f, C_GREEN);
    char pct[8];
    snprintf(pct, sizeof(pct), "%d%%", g_state.otaPct);
    text("Update", CX, 92, &fonts::DejaVu18, C_TEXT);
    text(pct, CX, 128, &fonts::DejaVu24, C_TEXT);
    text("nicht abstecken", CX, 158, &fonts::DejaVu12, C_DIM);
    if (g_tft) g_cv->pushSprite(0, g_offY);
    return;
  }
  bool waiting = !g_state.demo && (!g_state.linked() || !g_state.hasData || (s.h5 < 0 && s.d7 < 0));
  int stage = idleStage();
  updateIdle(waiting ? IDLE_ACTIVE : stage, now);
  if (waiting) {
    viewWaiting(now);
  } else if (stage == IDLE_ASLEEP) {
    viewSleep(s, now);
  } else {
    switch (g_view) {
      case VIEW_BUDDY: viewBuddy(s, now, stage); break;
      case VIEW_DETAILS: viewDetails(s); break;
      default: viewGauge(s, now); break;
    }
  }
  drawStatusBar();
  drawToast(now);
  if (g_tft) g_cv->pushSprite(0, g_offY);
}

void uiToast(const char* msg) {
  strlcpy(g_toast, msg, sizeof(g_toast));
  g_toastUntil = millis() + 1500;
}

void uiSetView(int v) {
  g_view = ((v % VIEW_COUNT) + VIEW_COUNT) % VIEW_COUNT;
}

int uiGetView() { return g_view; }

LGFX_Sprite* uiCanvas() { return g_cv; }

void uiDrawLogoFrame(float f) {
#if SHOW_BOOT_LOGO
  g_cv->fillScreen(C_BG);
  drawLogo(f, 112);
#endif
}

bool uiAsleep() {
  bool waiting = !g_state.demo && (!g_state.linked() || !g_state.hasData);
  return !waiting && idleStage() == IDLE_ASLEEP;
}

uint32_t uiStatusColor(bool& alarm, bool& idle) {
  Snapshot s = snap();
  idle = !g_state.demo && (!g_state.linked() || !g_state.hasData || s.h5 < 0 || uiAsleep());
  alarm = !idle && (s.h5 >= 90 || s.lim5 >= 0);
  if (idle) return C_SLEEP;
  return usageColor(s.h5);
}
