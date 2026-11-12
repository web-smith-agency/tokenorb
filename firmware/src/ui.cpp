#include "ui.h"
#include "config.h"
#include "state.h"
#if SHOW_BOOT_LOGO
#include "logo.h"
#endif

// =====================================================================
//  Oberflaeche: 240x240, rund. Alles wird in einen Sprite gezeichnet und
//  dann in einem Rutsch uebertragen -> kein Flackern.
//
//  Ab 1.3.0: Das Maskottchen besteht nur noch aus zwei leuchtenden Augen
//  (Farbe = Verbrauch). Dazu Claude-Aktivitaet (Hooks), neue Leerlauf-
//  Animationen, Emotes und Nachtmodus.
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
constexpr uint32_t C_INK    = 0x14171Bu;
constexpr uint32_t C_WHITE  = 0xFFFFFFu;
constexpr uint32_t C_SKY    = 0x93C5FDu;
constexpr uint32_t C_ROSE   = 0xE11D48u;
constexpr uint32_t C_PINK   = 0xFF5C8Au;
constexpr uint32_t C_VIOLET = 0xA78BFAu;
constexpr uint32_t C_BLUE   = 0x60A5FAu;
constexpr uint32_t C_GOLD   = 0xFCD34Du;
constexpr uint32_t C_MOON   = 0xFDE68Au;
constexpr uint32_t C_BUBBLE = 0xE5E9F0u;

inline int I(float v) { return (int)lroundf(v); }
inline float c01(float v) { return v < 0 ? 0 : (v > 1 ? 1 : v); }
inline float seg(float t, float a, float b) { return c01((t - a) / (b - a)); }
inline float bell(float t, float a, float b) { return sinf(seg(t, a, b) * PI); }
inline float easeOut(float t) { float u = 1 - t; return 1 - u * u * u; }
inline float easeIn(float t) { return t * t * t; }
inline float easeInOut(float t) { return t < 0.5f ? 4 * t * t * t : 1 - powf(-2 * t + 2, 3) / 2; }
inline float easeBack(float t) { const float c1 = 1.70158f, c3 = c1 + 1; return 1 + c3 * powf(t - 1, 3) + c1 * powf(t - 1, 2); }
inline float wave(uint32_t now, float period) { return 0.5f + 0.5f * sinf(now / period * TWO_PI); }
inline float rnd() { return (esp_random() & 0xFFFF) / 65535.0f; }
inline uint32_t hash32(uint32_t n) { n = (n ^ 61) ^ (n >> 16); n *= 9; n ^= n >> 4; n *= 0x27d4eb2d; n ^= n >> 15; return n; }

uint32_t mix(uint32_t a, uint32_t b, float t) {
  t = c01(t);
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
// Stoppuhr "m:ss" bzw. "h:mm:ss"
void fmtClock(uint32_t ms, char* out, size_t n) {
  uint32_t s = ms / 1000;
  if (s >= 3600) snprintf(out, n, "%lu:%02lu:%02lu", (unsigned long)(s / 3600), (unsigned long)((s / 60) % 60), (unsigned long)(s % 60));
  else snprintf(out, n, "%lu:%02lu", (unsigned long)(s / 60), (unsigned long)(s % 60));
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
  f0 = c01(f0);
  f1 = c01(f1);
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
void drawRings(float h5, float d7, float pace5, float pace7, bool thin = false,
               uint32_t track1 = C_TRACK, float dim = 0) {
  auto f = [dim](uint32_t c) { return dim > 0 ? mix(c, C_BG, dim) : c; };
  const int o1 = 117, i1 = thin ? 111 : 104;
  const int o2 = thin ? 107 : 98, i2 = thin ? 103 : 91;
  gaugeArc(i1, o1, 0, 1, f(track1));
  gaugeArc(i2, o2, 0, 1, f(C_TRACK2));
  if (h5 > 0.3f) gaugeArc(i1, o1, 0, h5 / 100.0f, f(usageColor(h5)));
  if (d7 > 0.3f) gaugeArc(i2, o2, 0, d7 / 100.0f, f(mix(usageColor(d7), C_TEXT, 0.15f)));
  if (dim <= 0) {
    paceTick(i1, o1, pace5);
    paceTick(i2, o2, pace7);
  }
}

// Komet im 5h-Ring: zeigt, dass Claude arbeitet
void comet(uint32_t now, float h5, float speed, float len = 64, uint32_t col = C_TEXT) {
  const int ri = 104, ro = 117, n = 8;
  float head = GAUGE_START + fmodf(now / 1000.0f * speed, GAUGE_SWEEP + len);
  for (int i = n - 1; i >= 0; i--) {
    float a1 = head - len * i / n, a0 = head - len * (i + 1) / n;
    float lo = fmaxf(a0, GAUGE_START), hi = fminf(a1, GAUGE_START + GAUGE_SWEEP);
    if (hi <= lo) continue;
    float f = ((lo + hi) * 0.5f - GAUGE_START) / GAUGE_SWEEP;
    uint32_t under = f < h5 / 100.0f ? usageColor(h5) : C_TRACK;
    arcRaw(ri, ro, lo, fminf(hi + 0.5f, GAUGE_START + GAUGE_SWEEP), mix(col, under, (i + 0.5f) / n));
  }
  if (head <= GAUGE_START + GAUGE_SWEEP) capAt(ri, ro, head, col);
}

// ---- Text-Helfer ------------------------------------------------------
void text(const char* s, int x, int y, const lgfx::IFont* f, uint32_t col,
          textdatum_t datum = middle_center) {
  g_cv->setFont(f);
  g_cv->setTextDatum(datum);
  g_cv->setTextColor(col);
  g_cv->drawString(s, x, y);
}
int textW(const char* s, const lgfx::IFont* f) {
  g_cv->setFont(f);
  return g_cv->textWidth(s);
}

// grosse Prozentzahl mit kleinem %-Zeichen, horizontal zentriert
void bigPercent(float pct, int baselineY, uint32_t col) {
  char num[8];
  if (pct < 0) snprintf(num, sizeof(num), "--");
  else snprintf(num, sizeof(num), "%d", (int)roundf(pct));
  int w1 = textW(num, &fonts::DejaVu56);
  int w2 = pct < 0 ? 0 : textW("%", &fonts::DejaVu24);
  int x = CX - (w1 + w2 + 2) / 2;
  text(num, x, baselineY, &fonts::DejaVu56, col, baseline_left);
  if (pct >= 0) text("%", x + w1 + 2, baselineY, &fonts::DejaVu24, C_DIM, baseline_left);
}

// Unten enden die Ringe: bei y 170..195 sind nur ca. 122 px frei, sonst ueberdecken die
// Ring-Enden den Text. Zu breite Texte werden deshalb kleiner gesetzt.
constexpr int BOTTOM_W = 122;
void fitText(const char* s, int y, uint32_t col) {
  if (textW(s, &fonts::DejaVu18) <= BOTTOM_W) text(s, CX, y, &fonts::DejaVu18, col);
  else text(s, CX, y, &fonts::DejaVu12, col);
}
// Zeile unten in der Buddy-Ansicht (gross) + kleine Unterzeile
void label(const char* s, uint32_t col = C_TEXT) { fitText(s, 186, col); }
void sublabel(const char* s, uint32_t col = C_DIM) { text(s, CX, 205, &fonts::DejaVu12, col); }
void labelDots(const char* s, uint32_t now, uint32_t col = C_TEXT) {
  int w = textW(s, &fonts::DejaVu18), x0 = CX - 9;
  text(s, x0, 186, &fonts::DejaVu18, col);
  int n = (now / 350) % 4;
  for (int i = 0; i < 3; i++) g_cv->fillCircle(x0 + w / 2 + 5 + i * 6, 191, 2, i < n ? col : mix(col, C_BG, 0.72f));
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

// ---- kleine Formen ------------------------------------------------------
void tri(float x0, float y0, float x1, float y1, float x2, float y2, uint32_t c) {
  g_cv->fillTriangle(I(x0), I(y0), I(x1), I(y1), I(x2), I(y2), c);
}
void quad(float x0, float y0, float x1, float y1, float x2, float y2, float x3, float y3, uint32_t c) {
  tri(x0, y0, x1, y1, x2, y2, c);
  tri(x0, y0, x2, y2, x3, y3, c);
}
void circ(float x, float y, float r, uint32_t c) { if (r > 0.4f) g_cv->fillCircle(I(x), I(y), I(fmaxf(1, r)), c); }
void ell(float x, float y, float rx, float ry, uint32_t c) {
  if (rx > 0.4f && ry > 0.4f) g_cv->fillEllipse(I(x), I(y), I(fmaxf(1, rx)), I(fmaxf(1, ry)), c);
}
void rrect(float x, float y, float w, float h, float r, uint32_t c) {
  if (w >= 1 && h >= 1) g_cv->fillRoundRect(I(x), I(y), I(w), I(h), I(fminf(r, fminf(w, h) / 2)), c);
}
void sparkle(float x, float y, float s, uint32_t c) {
  float q = s * 0.28f;
  float p[8][2] = {{x, y - s}, {x + q, y - q}, {x + s, y}, {x + q, y + q}, {x, y + s}, {x - q, y + q}, {x - s, y}, {x - q, y - q}};
  for (int i = 0; i < 8; i++) tri(x, y, p[i][0], p[i][1], p[(i + 1) % 8][0], p[(i + 1) % 8][1], c);
}
void heart(float x, float y, float s, uint32_t c) {
  circ(x - s * 0.5f, y, s * 0.56f, c);
  circ(x + s * 0.5f, y, s * 0.56f, c);
  tri(x - s * 1.04f, y + s * 0.2f, x + s * 1.04f, y + s * 0.2f, x, y + s * 1.15f, c);
}
void drop(float x, float y, float s, uint32_t c) {
  tri(x, y - 9 * s, x - 5 * s, y, x + 5 * s, y, c);
  circ(x, y + s, 5 * s, c);
}
void note(int x, int y, uint32_t col) {
  g_cv->fillEllipse(x, y, 4, 3, col);
  g_cv->fillRect(x + 3, y - 11, 2, 11, col);
  g_cv->fillTriangle(x + 4, y - 11, x + 10, y - 7, x + 4, y - 6, col);
}
// Sprechblase mit kleinem Zipfel Richtung (tx, ty)
void bubble(float x, float y, float w, float h, uint32_t col, const char* s, uint32_t tcol, float tx, float ty) {
  tri(x - w * 0.2f, y + h * 0.3f, x + w * 0.05f, y + h * 0.42f, tx, ty, col);
  rrect(x - w / 2, y - h / 2, w, h, fminf(w, h) * 0.45f, col);
  if (s && *s) text(s, I(x), I(y + 1), h >= 22 ? &fonts::DejaVu18 : &fonts::DejaVu12, tcol);
}
void zzz(uint32_t now, float cx, float cy, float r) {
  for (int i = 0; i < 3; i++) {
    float ph = fmodf(now / 2200.0f + i / 3.0f, 1.0f);
    int zx = cx + r * 0.65f + ph * 22 + sinf(ph * 6.0f) * 3;
    int zy = cy - r * 0.55f - ph * 36;
    const lgfx::IFont* f = (i == 0) ? &fonts::DejaVu18 : (i == 1 ? &fonts::DejaVu12 : &fonts::DejaVu9);
    text("z", zx, zy, f, mix(C_SLEEP, C_BG, ph));
  }
}

// =====================================================================
//  Das Gesicht: zwei leuchtende Augen + kleiner Mund, ohne Koerper
// =====================================================================
enum EyeT : uint8_t { EYE_OPEN, EYE_CLOSED, EYE_HAPPY, EYE_SADC, EYE_SPIRAL, EYE_HEART };
enum MouthT : uint8_t { M_NONE, M_SMILE, M_BIGSMILE, M_SMIRK, M_GRIN, M_FLAT, M_SMALL, M_HMM, M_O,
                        M_FROWN, M_WOBBLE, M_TONGUE, M_CAT, M_WHISTLE, M_YAWN };
enum BrowT : uint8_t { BROW_NONE, BROW_WORRIED };

struct Face {
  float x = CX, y = 104, r = 44;
  uint32_t col = C_GREEN;
  EyeT eyeL = EYE_OPEN, eyeR = EYE_OPEN;
  bool autoLook = true;
  float lookX = 0, lookY = 0, lookRange = 1, turn = 1;
  float eyeH = 1, lid = 0, droop = 0, yawn = 0;
  float scaleL = 1, scaleR = 1;
  bool wide = false, blink = true, blush = false;
  BrowT brows = BROW_NONE;
  MouthT mouth = M_SMILE;
  float mouthAmt = 1;
  void eyes(EyeT e) { eyeL = eyeR = e; }
  void look(float x, float y) { autoLook = false; lookX = x; lookY = y; }
};
struct Geo {
  float cx, cy, r, k, fcx, ex, ey, ew, eh, mx, my;
  uint32_t col;
};

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

// Blickspruenge: alle 0,6..2,8 s ein neues Ziel, ein Drittel der Zeit geradeaus
struct LookState { float fx = 0, fy = 0, tx = 0, ty = 0; uint32_t ls = 0, ln = 0; } g_look;
void lookCur(uint32_t now, float& lx, float& ly) {
  float p = easeOut(c01((now - g_look.ls) / 110.0f));
  lx = g_look.fx + (g_look.tx - g_look.fx) * p;
  ly = g_look.fy + (g_look.ty - g_look.fy) * p;
}
void lookAuto(uint32_t now, float range, float& lx, float& ly) {
  if ((int32_t)(now - g_look.ln) >= 0) {
    float cx, cy;
    lookCur(now, cx, cy);
    g_look.fx = cx; g_look.fy = cy;
    if (esp_random() % 100 < 32) { g_look.tx = 0; g_look.ty = 0; }
    else { g_look.tx = (rnd() * 2 - 1) * 0.9f * range; g_look.ty = (rnd() * 2 - 1) * 0.55f * range; }
    g_look.ls = now;
    g_look.ln = now + 600 + esp_random() % 2200;
  }
  lookCur(now, lx, ly);
}

void glowEye(const Geo& g, const Face& f, float x, float y, float w, float h, EyeT type, int side,
             float bl, float lx, float ly, uint32_t now) {
  const float k = g.k;
  const uint32_t col = g.col;
  switch (type) {
    case EYE_CLOSED: rrect(x - w, y - 1.6f * k, 2 * w, 3.4f * k, 1.7f * k, col); return;
    case EYE_SADC: g_cv->fillArc(I(x), I(y - h * 0.3f), I(w * 1.05f), I(w * 1.05f - 3.6f * k), 25, 155, col); return;
    case EYE_SPIRAL: {
      float rot = now / 140.0f * side, px = x, py = y;
      for (float a = 0.3f; a <= 4 * PI; a += 0.3f) {
        float q = w * 0.95f * a / (4 * PI);
        float nx = x + cosf(a + rot) * q, ny = y + sinf(a + rot) * q * (h / w) * 0.8f;
        g_cv->drawWideLine(px, py, nx, ny, 1.2f * k, col);
        px = nx; py = ny;
      }
      return;
    }
    case EYE_HEART: {
      float s = w * 1.05f * (1 + 0.12f * sinf(now / 170.0f));
      heart(x, y - s * 0.3f, s, C_PINK);
      return;
    }
    default: break;
  }
  float per = 1 + side * lx * 0.14f, wd = f.wide ? 1.12f : 1;
  float W = w * per * wd, Hf = h * per * wd * f.eyeH;
  float xx = x + lx * g.r * 0.07f, yy = y + ly * g.r * 0.06f;
  float hh = fmaxf(2.5f * k, Hf * (1 - bl) * (1 - 0.75f * f.yawn));
  rrect(xx - W, yy - hh, 2 * W, 2 * hh, fminf(W * 0.6f, hh), col);
  if (type == EYE_HAPPY) { ell(xx, yy + hh * 1.15f, W * 1.7f, hh * 0.95f, C_BG); return; }
  if (hh > 5 * k) rrect(xx - W * 0.62f, yy - hh * 0.72f, W * 0.42f, hh * 0.28f, W * 0.14f, mix(col, C_WHITE, 0.55f));
  float lid = fmaxf(f.lid, f.droop * 0.7f);
  if (lid > 0.02f) g_cv->fillRect(I(xx - W - 1), I(yy - hh - 1), I(W * 2 + 2), I(hh * 2 * lid + 1), C_BG);
  if (f.brows == BROW_WORRIED) {
    float xo = xx + side * (W + 1), xi = xx - side * (W + 1), top = yy - hh - 1;
    tri(xo, top, xi, top, xo, yy - hh * 0.05f, C_BG);
  }
}

void drawMouth(const Geo& g, const Face& f, uint32_t now) {
  const float k = g.k, x = g.mx, y = g.my, a = f.mouthAmt;
  const uint32_t ink = g.col;
  switch (f.mouth) {
    case M_SMILE: g_cv->fillArc(I(x), I(y - 6 * k), I(11 * k), I(8 * k), 20, 160, ink); break;
    case M_BIGSMILE: g_cv->fillArc(I(x), I(y - 8 * k), I(14 * k), I(10.5f * k), 15, 165, ink); break;
    case M_SMIRK: g_cv->fillArc(I(x + 3 * k), I(y - 7 * k), I(11 * k), I(8 * k), 15, 115, ink); break;
    case M_GRIN:
      g_cv->fillArc(I(x), I(y - 3 * k), I(12 * k * a), 0, 0, 180, ink);
      ell(x, y + 4.5f * k * a, 5.5f * k * a, 2.8f * k * a, C_ROSE);
      break;
    case M_FLAT: rrect(x - 9 * k, y, 18 * k, 3 * k, 1.5f * k, ink); break;
    case M_SMALL: rrect(x - 5 * k, y, 10 * k, 3 * k, 1.5f * k, ink); break;
    case M_HMM: g_cv->drawWideLine(x - 5 * k, y + 2 * k, x + 6 * k, y - 0.5f * k, 1.5f * k, ink); break;
    case M_O: ell(x, y + 2 * k, 5 * k * a, 6 * k * a, ink); break;
    case M_FROWN: g_cv->fillArc(I(x), I(y + 9 * k), I(10 * k), I(7 * k), 205, 335, ink); break;
    case M_WOBBLE: {
      float p[5][2] = {{x - 9 * k, y + 2 * k}, {x - 4.5f * k, y - k}, {x, y + 2 * k}, {x + 4.5f * k, y - k}, {x + 9 * k, y + 2 * k}};
      for (int i = 0; i < 4; i++) g_cv->drawWideLine(p[i][0], p[i][1], p[i + 1][0], p[i + 1][1], 1.4f * k, ink);
      break;
    }
    case M_TONGUE:
      rrect(x - 7 * k, y, 14 * k, 3 * k, 1.5f * k, ink);
      ell(x + 3 * k, y + 5.5f * k, 4 * k, 4.5f * k, C_ROSE);
      break;
    case M_CAT:
      g_cv->fillArc(I(x - 4 * k), I(y), I(4.5f * k), I(2.6f * k), 0, 180, ink);
      g_cv->fillArc(I(x + 4 * k), I(y), I(4.5f * k), I(2.6f * k), 0, 180, ink);
      break;
    case M_WHISTLE:
      circ(x + 3 * k, y + k, 4 * k, ink);
      for (int i = 0; i < 2; i++) {
        float ph = fmodf(now / 1300.0f + i * 0.5f, 1.0f);
        note(x + g.r * 0.6f + ph * 20, g.cy - g.r * 0.15f - ph * 30, mix(C_TEXT, C_BG, ph));
      }
      break;
    case M_YAWN:
      ell(x, y + 2 * k, (6 + 5 * f.yawn) * k, (3 + 11 * f.yawn) * k, ink);
      ell(x, y + (4 + 6 * f.yawn) * k, (3 + 3 * f.yawn) * k, (1 + 3 * f.yawn) * k, C_ROSE);
      break;
    default: break;
  }
}

Geo drawFace(const Face& f, uint32_t now) {
  float lx, ly;
  if (f.autoLook) lookAuto(now, f.lookRange, lx, ly);
  else { lx = f.lookX; ly = f.lookY; }
  Geo g;
  g.r = f.r; g.k = f.r / 44.0f; g.cx = f.x; g.cy = f.y;
  float fx = lx * f.r * 0.10f * f.turn, fy = ly * f.r * 0.07f * f.turn;
  g.col = mix(f.col, C_WHITE, 0.12f);
  g.fcx = f.x + fx;
  g.ex = f.r * 0.44f; g.ew = f.r * 0.23f; g.eh = f.r * 0.34f;   // ohne Koerper etwas groesser als im Board
  g.ey = f.y - f.r * 0.06f + fy;
  g.mx = f.x + fx * 0.85f;
  g.my = f.y + f.r * 0.44f + fy * 0.7f;
  float bl = f.blink ? blinkAmount(now) : 0;
  glowEye(g, f, g.fcx - g.ex, g.ey, g.ew * f.scaleL, g.eh * f.scaleL, f.eyeL, -1, bl, lx, ly, now);
  glowEye(g, f, g.fcx + g.ex, g.ey, g.ew * f.scaleR, g.eh * f.scaleR, f.eyeR, 1, bl, lx, ly, now);
  drawMouth(g, f, now);
  if (f.blush) {
    uint32_t bc = mix(f.col, 0xFF6B9Au, 0.5f);
    ell(g.fcx - f.r * 0.62f, g.my - 5 * g.k, 6 * g.k, 3.4f * g.k, bc);
    ell(g.fcx + f.r * 0.62f, g.my - 5 * g.k, 6 * g.k, 3.4f * g.k, bc);
  }
  return g;
}

// ---- Requisiten ---------------------------------------------------------
void thought(uint32_t now, float x, float y) {
  circ(x - 19, y + 28, 2.4f, C_BUBBLE); circ(x - 12, y + 18, 3.4f, C_BUBBLE);
  circ(x - 8, y + 2, 10, C_BUBBLE); circ(x + 8, y + 1, 11, C_BUBBLE);
  circ(x, y - 7, 10, C_BUBBLE); circ(x, y + 6, 9, C_BUBBLE);
  for (int i = 0; i < 3; i++) {
    float b = fmaxf(0, sinf(now / 220.0f - i * 0.9f));
    circ(x - 7 + i * 7, y + 1 - b * 3, 2.2f, C_INK);
  }
}
void miniOrb(float x, float y, float z, float a, uint32_t c) {
  float s = 0.82f + 0.18f * (z + 1) / 2, r = 11 * s;
  uint32_t body = z < 0 ? mix(c, C_BG, 0.3f) : c;
  circ(x, y, r, mix(body, C_BG, 0.25f));
  circ(x - 0.6f, y - 1, r - 1.2f, body);
  float dir = -sinf(a) > 0 ? 1 : -1;
  for (int sd = -1; sd <= 1; sd += 2) {
    ell(x + sd * r * 0.38f + dir * 1.2f, y - r * 0.1f, r * 0.2f, r * 0.28f, C_WHITE);
    circ(x + sd * r * 0.38f + dir * 2, y - r * 0.08f, r * 0.12f, C_INK);
  }
}
void moon(float x, float y, float r) { circ(x, y, r, C_MOON); circ(x + r * 0.45f, y - r * 0.3f, r * 0.85f, C_BG); }
void nightcap(const Face& f) {
  const float cx = f.x, cy = f.y + f.r * 0.12f, r = f.r;
  auto X = [&](float u) { return cx + u * r; };
  auto Y = [&](float v) { return cy + v * r; };
  const uint32_t cap = 0x3B4A8Cu;
  // Kegel (konvexes Fuenfeck als Faecher) + Zipfel
  float px[5] = {X(-0.7f), X(0.62f), X(0.12f), X(-0.6f), X(-1.02f)};
  float py[5] = {Y(-0.68f), Y(-0.76f), Y(-1.25f), Y(-1.17f), Y(-0.78f)};
  for (int i = 1; i < 4; i++) tri(px[0], py[0], px[i], py[i], px[i + 1], py[i + 1], cap);
  tri(X(-0.6f), Y(-1.17f), X(-1.02f), Y(-0.78f), X(-1.16f), Y(-0.24f), cap);
  g_cv->drawWideLine(X(-0.35f), Y(-1.05f), X(0.25f), Y(-1.12f), 0.8f, 0x2E3A70u);
  quad(X(-0.74f), Y(-0.73f), X(0.66f), Y(-0.81f), X(0.7f), Y(-0.62f), X(-0.72f), Y(-0.53f), 0xE2E8F0u);
  circ(X(-1.16f), Y(-0.24f), r * 0.13f, 0xF1F5F9u);
}
void partyHat(const Face& f) {
  const float cx = f.x, cy = f.y + f.r * 0.1f, r = f.r;
  float tx = cx - r * 0.22f, ty = cy - r * 1.72f, ax = cx - r * 0.66f, ay = cy - r * 0.74f, bx = cx + r * 0.24f, by = cy - r * 0.97f;
  tri(ax, ay, bx, by, tx, ty, C_VIOLET);
  for (int i = 1; i <= 3; i++) {
    float t = i / 4.2f;
    circ((ax + bx) / 2 + (tx - (ax + bx) / 2) * t + (i % 2 ? -3 : 3), (ay + by) / 2 + (ty - (ay + by) / 2) * t, 2.2f, C_GOLD);
  }
  circ(tx, ty, r * 0.12f, C_GOLD);
}
const uint32_t CONF[6] = {C_GREEN, C_AMBER, C_BLUE, C_PINK, C_VIOLET, C_TEXT};
void confettiRain(uint32_t now) {
  for (int i = 0; i < 16; i++) {
    float x = 44 + (hash32(i * 13 + 1) % 1000) / 1000.0f * 152;
    float sp = 45 + (hash32(i * 7 + 3) % 1000) / 1000.0f * 55;
    float y = fmodf(now / 1000.0f * sp + (hash32(i + 77) % 240), 240.0f) - 8;
    float sw = sinf(now / 420.0f + i) * 4, fl = fabsf(cosf(now / 190.0f + i));
    if (i % 3 == 0) circ(x + sw, y, 2, CONF[i % 6]);
    else g_cv->fillRect(I(x + sw - 2), I(y - 1 - fl * 1.5f), 4, I(2 + fl * 3), CONF[i % 6]);
  }
}
void book(uint32_t now, float x, float y) {
  const float w = 38, h = 34;
  rrect(x - w - 3, y - h / 2 - 2, 2 * w + 6, h + 5, 3, 0x1E40AFu);
  quad(x - w, y - h / 2 + 1, x, y - h / 2 + 3, x, y + h / 2, x - w, y + h / 2 - 1, 0xF1F5F9u);
  quad(x, y - h / 2 + 3, x + w, y - h / 2 + 1, x + w, y + h / 2 - 1, x, y + h / 2, 0xE2E8F0u);
  for (int i = 0; i < 4; i++) {
    g_cv->fillRect(I(x - w + 5), I(y - h / 2 + 9 + i * 6), I(w - 10), 1, 0xA3AEC0u);
    g_cv->fillRect(I(x + 5), I(y - h / 2 + 9 + i * 6), I(w - 10), 1, 0xA3AEC0u);
  }
  float f = fmodf(now / 4000.0f, 1.0f);
  if (f > 0.8f) {
    float q = (f - 0.8f) / 0.2f, ex = x + w * cosf(q * PI), lift = sinf(q * PI) * 4;
    quad(x, y - h / 2 + 3, ex, y - h / 2 + 1 - lift, ex, y + h / 2 - 1 - lift, x, y + h / 2, q < 0.5f ? 0xF8FAFCu : 0xEEF2F7u);
  }
  g_cv->fillRect(I(x - 0.6f), I(y - h / 2 + 3), 1, I(h - 3), 0x94A3B8u);
}

// =====================================================================
//  Zustand: Leerlauf, Aktivitaet, Emotes
// =====================================================================
enum IdleStage { IDLE_ACTIVE = 0, IDLE_BORED, IDLE_TIRED, IDLE_ASLEEP };
enum IdleAnim { ANIM_NONE, ANIM_LOOK, ANIM_WHISTLE, ANIM_HOP, ANIM_YAWN, ANIM_PEEK, ANIM_READ };
IdleAnim g_anim = ANIM_NONE;
uint32_t g_animStart = 0, g_animNext = 0, g_wakeAt = 0;
int g_prevStage = IDLE_ACTIVE;
uint32_t animLen(IdleAnim a) { return a == ANIM_PEEK ? 7600 : (a == ANIM_READ ? 9000 : 3200); }

bool isNight() {
  int m = g_state.localMinute();
  if (m < 0) return false;
  return NIGHT_FROM_MIN > NIGHT_TO_MIN ? (m >= NIGHT_FROM_MIN || m < NIGHT_TO_MIN) : (m >= NIGHT_FROM_MIN && m < NIGHT_TO_MIN);
}

int idleStage() {
  if (g_state.demo) return IDLE_ACTIVE;
  uint32_t i = g_state.idleMs();
  if (i >= IDLE_SLEEP_MS || (isNight() && i >= NIGHT_SLEEP_MS)) return IDLE_ASLEEP;
  if (i >= IDLE_TIRED_MS) return IDLE_TIRED;
  if (i >= IDLE_BORED_MS) return IDLE_BORED;
  return IDLE_ACTIVE;
}

void updateIdle(int stage, uint32_t now) {
  if (g_prevStage == IDLE_ASLEEP && stage < IDLE_ASLEEP) g_wakeAt = now;   // aufgewacht
  g_prevStage = stage;
  if (stage != IDLE_BORED) { g_anim = ANIM_NONE; return; }
  if (g_anim != ANIM_NONE && now - g_animStart > animLen(g_anim)) g_anim = ANIM_NONE;
  if (g_anim == ANIM_NONE && (int32_t)(now - g_animNext) >= 0) {
    uint32_t r = esp_random() % 100;
    g_anim = r < 22 ? ANIM_LOOK : r < 40 ? ANIM_WHISTLE : r < 55 ? ANIM_HOP : r < 65 ? ANIM_YAWN : r < 82 ? ANIM_PEEK : ANIM_READ;
    g_animStart = now;
    g_animNext = now + animLen(g_anim) + 5000 + esp_random() % 9000;
  }
}

// Was zeigt der Orb fuer die Claude-Aktivitaet gerade?
enum Show : uint8_t { SH_NONE, SH_THINK, SH_TOOL, SH_SUB, SH_PERM, SH_INPUT, SH_COMPACT, SH_DONE, SH_ERR, SH_HELLO, SH_BYE };
constexpr uint32_t DONE_MS = 2000, ERR_MS = 4200, HELLO_MS = 2600, BYE_MS = 2300;

struct Cur { Show sh; uint32_t el; };
Cur curActivity(uint32_t now) {
  Activity& a = g_state.act;
  if (a.act != Act::Idle && now - a.refreshMs > ACT_TIMEOUT_MS) a.act = Act::Idle;   // Sicherheitsnetz
  if (!g_state.linked() || g_state.demo) return {SH_NONE, 0};
  uint32_t ev = now - a.evtMs;
  if (a.act == Act::Perm) return {SH_PERM, now - a.sinceMs};
  if (a.evt == ActEvt::Err && ev < ERR_MS) return {SH_ERR, ev};
  if (a.evt == ActEvt::Done && ev < DONE_MS) return {SH_DONE, ev};
  if (a.evt == ActEvt::Hello && ev < HELLO_MS) return {SH_HELLO, ev};
  if (a.evt == ActEvt::Bye && ev < BYE_MS) return {SH_BYE, ev};
  switch (a.act) {
    case Act::Think: return {SH_THINK, now - a.sinceMs};
    case Act::Tool: return {SH_TOOL, now - a.sinceMs};
    case Act::Sub: return {SH_SUB, now - a.sinceMs};
    case Act::Compact: return {SH_COMPACT, now - a.sinceMs};
    case Act::Input: return {SH_INPUT, now - a.sinceMs};
    default: return {SH_NONE, 0};
  }
}

// Emotes: kurze Reaktionen in der Buddy-Ansicht
enum Emote : uint8_t { EM_NONE, EM_PARTY, EM_WEEK, EM_WORRIED, EM_PANIC, EM_LOVE, EM_WINK };
Emote g_emote = EM_NONE;
uint32_t g_emoteAt = 0, g_winkAt = 0;
float g_prevH5 = -1, g_prevD7 = -1;
int32_t g_prevLim = -1;
uint32_t emoteLen(Emote e) {
  switch (e) {
    case EM_PARTY: case EM_WEEK: return 6000;
    case EM_LOVE: return 3600;
    case EM_WINK: return 1600;
    default: return 4000;
  }
}
void startEmote(Emote e, uint32_t now) { g_emote = e; g_emoteAt = now; }

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

// Emotes aus den Limit-Werten ausloesen
void checkEmotes(const Snapshot& s, uint32_t now) {
  if (g_state.demo) { g_prevH5 = -1; return; }
  if (g_prevH5 >= 0 && s.h5 >= 0) {
    if (g_prevH5 >= 20 && s.h5 < 5) startEmote(EM_PARTY, now);              // 5h-Fenster zurueckgesetzt
    else if (g_prevH5 < 80 && s.h5 >= 80 && s.h5 < 100) startEmote(EM_WORRIED, now);
  }
  if (g_prevD7 >= 20 && s.d7 >= 0 && s.d7 < 5) startEmote(EM_WEEK, now);   // neue Woche
  if (g_prevLim < 0 && s.lim5 >= 0 && g_prevH5 >= 0) startEmote(EM_PANIC, now);
  g_prevH5 = s.h5;
  g_prevD7 = s.d7;
  g_prevLim = s.lim5;
}

// ---- Buddy-Ansicht: Szenen -------------------------------------------------
// "82%" gross, Restzeit klein daneben – passt zwischen die Ring-Enden
void usageLine(const Snapshot& s) {
  char p[8], d[16];
  if (s.h5 < 0) { text("--", CX, 182, &fonts::DejaVu18, C_TEXT); return; }
  snprintf(p, sizeof(p), "%d%%", (int)roundf(s.h5));
  fmtDur(s.r5, d, sizeof(d));
  int w1 = textW(p, &fonts::DejaVu18), w2 = textW(d, &fonts::DejaVu12), gap = 8;
  int x = CX - (w1 + gap + w2) / 2;
  text(p, x, 189, &fonts::DejaVu18, C_TEXT, baseline_left);
  text(d, x + w1 + gap, 189, &fonts::DejaVu12, C_DIM, baseline_left);
}
uint32_t eyeColor(const Snapshot& s) { return usageColor(s.h5 < 0 ? 0 : g_dispH5); }
void ringsNormal(const Snapshot& s, uint32_t track1 = C_TRACK) { drawRings(g_dispH5, g_dispD7, s.pace5, s.pace7, false, track1); }

// "seit m:ss" unter Denken/Helfer
void sinceLabel(uint32_t el) {
  char c[16], buf[24];
  fmtClock(el, c, sizeof(c));
  snprintf(buf, sizeof(buf), "seit %s", c);
  sublabel(buf);
}

void sceneThink(const Snapshot& s, uint32_t now, uint32_t el) {
  ringsNormal(s);
  comet(now, g_dispH5, 75);
  static float lx = 0, ly = -0.8f;
  int ph = (now / 2600) % 2;
  lx += ((ph ? 0.75f : -0.55f) - lx) * 0.18f;
  ly += (-0.85f - ly) * 0.18f;
  Face f;
  f.x = 110; f.y = 114 + sinf(now / 1100.0f) * 2.5f; f.r = 36; f.col = eyeColor(s);
  f.look(lx, ly); f.mouth = M_HMM; f.eyeH = 0.92f;
  drawFace(f, now);
  thought(now, 158, 60);
  labelDots("denkt nach", now);
  sinceLabel(el);
}

void sceneSub(const Snapshot& s, uint32_t now, uint32_t el) {
  ringsNormal(s);
  comet(now, g_dispH5, 120);
  int n = constrain((int)g_state.act.subs, 1, 3);
  const uint32_t cols[3] = {C_VIOLET, C_BLUE, C_AMBER};
  float mx[3], my[3], mz[3], ma[3];
  for (int i = 0; i < n; i++) {
    ma[i] = now / 4200.0f * TWO_PI + i * TWO_PI / n;
    mx[i] = CX + cosf(ma[i]) * 64; my[i] = 102 + sinf(ma[i]) * 18; mz[i] = sinf(ma[i]);
  }
  for (int i = 0; i < n; i++) if (mz[i] < 0) miniOrb(mx[i], my[i], mz[i], ma[i], cols[i]);
  int t = 0;
  for (int i = 0; i < n; i++) if (mz[i] >= 0) { t = i; break; }
  Face f;
  f.r = 36; f.col = eyeColor(s); f.turn = 0.6f;
  f.look(constrain((mx[t] - CX) / 50.0f, -1.0f, 1.0f), constrain((my[t] - 100) / 30.0f, -0.6f, 0.6f));
  drawFace(f, now);
  for (int i = 0; i < n; i++) if (mz[i] >= 0) miniOrb(mx[i], my[i], mz[i], ma[i], cols[i]);
  char buf[24];
  if (g_state.act.subs <= 1) snprintf(buf, sizeof(buf), "1 Helfer");
  else snprintf(buf, sizeof(buf), "%d Helfer", g_state.act.subs);
  label(buf);
  sinceLabel(el);
}

void scenePerm(const Snapshot& s, uint32_t now) {
  float pulse = 0.5f + 0.5f * sinf(now / 600.0f * PI);
  ringsNormal(s, mix(C_TRACK, C_AMBER, 0.18f + 0.42f * pulse));
  Face f;
  f.x = 116; f.y = 110; f.r = 38; f.col = eyeColor(s);
  f.look(0, 0.05f); f.wide = true; f.mouth = M_O; f.mouthAmt = 0.65f; f.turn = 0.4f;
  drawFace(f, now);
  float pop = 1 + 0.08f * sinf(now / 600.0f * PI);
  bubble(160, 66, 26 * pop, 24 * pop, C_AMBER, "?", C_INK, 146, 86);
  label("Du bist dran", C_AMBER);
  char buf[32];
  if (g_state.act.detail[0]) snprintf(buf, sizeof(buf), "%s erlauben?", g_state.act.detail);
  else snprintf(buf, sizeof(buf), "Erlaubnis noetig");
  sublabel(buf);
}

void sceneInput(const Snapshot& s, uint32_t now, uint32_t el) {
  ringsNormal(s);
  Face f;
  f.y = 106; f.r = 42; f.col = eyeColor(s); f.turn = 0.5f;
  if (fmodf(now / 5200.0f, 1.0f) < 0.73f) f.look(0, 0.05f); else f.look(0.6f, -0.3f);
  drawFace(f, now);
  bubble(164, 64, 30, 18, 0x2A3039u, nullptr, 0, 152, 80);
  for (int i = 0; i < 3; i++) {
    float b = fmaxf(0, sinf(now / 260.0f - i * 0.9f));
    circ(156 + i * 8, 64 - b * 2, 2.2f, C_DIM);
  }
  label("Du bist dran", C_DIM);
  char d[16], buf[32];
  fmtDur(el / 1000 + 60, d, sizeof(d));   // idle_prompt kommt nach ca. 60 s Warten
  snprintf(buf, sizeof(buf), "wartet seit %s", d);
  sublabel(buf, C_FAINT);
}

void sceneDone(const Snapshot& s, uint32_t now, uint32_t el) {
  ringsNormal(s, mix(C_TRACK, C_GREEN, 0.5f * bell(el, 250, 750)));
  float lift = el > 200 && el < 700 ? sinf((el - 200) / 500.0f * PI) * 16 : 0;
  Face f;
  f.y = 106 - lift; f.col = eyeColor(s); f.look(0, 0);
  if (el > 120) { f.eyes(EYE_HAPPY); f.mouth = el < 1400 ? M_GRIN : M_BIGSMILE; f.blush = true; }
  drawFace(f, now);
  label("Fertig!", C_GREEN);
}

void sceneError(const Snapshot& s, uint32_t now, uint32_t el) {
  bool fl = el < 600 && (el / 100) % 2;
  ringsNormal(s, fl ? mix(C_TRACK, C_RED, 0.6f) : C_TRACK);
  float shake = el < 600 ? sinf(el / 22.0f) * 4 * (1 - el / 600.0f) : 0;
  Face f;
  f.x = CX + shake; f.y = 108; f.r = 40; f.col = eyeColor(s);
  f.eyes(EYE_SPIRAL); f.mouth = M_WOBBLE; f.look(0, 0); f.turn = 0;
  drawFace(f, now);
  for (int i = 0; i < 3; i++) {
    float a = now / 600.0f + i * TWO_PI / 3;
    sparkle(CX + cosf(a) * 34, 60 + sinf(a) * 7, 5.5f + 1.5f * sinf(a), C_GOLD);
  }
  label("Fehler", C_RED);
  const char* d = g_state.act.detail;
  const char* why = !strcmp(d, "rate_limit") ? "Rate-Limit" : !strcmp(d, "overloaded") ? "API ueberlastet"
                  : !strcmp(d, "server_error") ? "Serverfehler" : !strcmp(d, "authentication_failed") ? "Anmeldung"
                  : !strcmp(d, "billing_error") ? "Abrechnung" : "API-Fehler";
  sublabel(why);
}

void sceneCompact(const Snapshot& s, uint32_t now) {
  ringsNormal(s);
  comet(now, g_dispH5, 90);
  float gulp = bell(fmodf(now / 1400.0f, 1.0f), 0.78f, 1.0f);
  Face f;
  f.r = 40; f.col = eyeColor(s); f.eyeH = 0.55f; f.look(0, 0.1f); f.turn = 0;
  f.mouth = M_O; f.mouthAmt = 1.1f + 0.3f * gulp;
  Geo g = drawFace(f, now);
  static const char* gl[10] = {"{", "}", "a", "#", "1", "x", "<", ">", "=", ";"};
  for (int i = 0; i < 10; i++) {
    float ph = fmodf(now / 1700.0f + i / 10.0f, 1.0f);
    float ang = i * 2.4f + 0.3f + ph * 2.2f, rad = (1 - easeIn(ph)) * 80;
    float x = g.mx + cosf(ang) * rad, y = g.my + 2 + sinf(ang) * rad * 0.75f;
    if (rad < 7 || y > 168) continue;
    text(gl[i], I(x), I(y), &fonts::DejaVu12, mix(C_DIM, C_TEXT, ph));
  }
  label("raeumt auf");
  sublabel("Kontext verdichten");
}

void sceneHello(const Snapshot& s, uint32_t now, uint32_t L) {
  ringsNormal(s);
  float y = 106;
  if (L < 600) y = 215 + (106 - 215) * easeBack(L / 600.0f);
  else if (L > 4500) y = 106 + (235 - 106) * easeIn(seg(L, 4500, 5100));
  bool bye = L > 2900, waving = (L > 500 && L < 2300) || (L > 2900 && L < 4600);
  if (y < 230) {
    Face f;
    f.y = y; f.r = 40; f.col = eyeColor(s); f.look(0, 0); f.mouth = M_BIGSMILE;
    if (waving && !bye) f.eyes(EYE_HAPPY);
    drawFace(f, now);
  }
  if (waving) {
    const char* t = bye ? "Tschuess!" : "Hallo!";
    bubble(CX, 50, textW(t, &fonts::DejaVu12) + 20, 20, C_TEXT, t, C_INK, 112, 66);
  }
  label(bye ? "Sitzung aus" : "Neue Sitzung");
}

// ---- Emotes ----
void emoteParty(const Snapshot& s, uint32_t now, uint32_t L, bool week) {
  float h5 = L < 1200 ? 100 + (s.h5 - 100) * easeInOut(L / 1200.0f) : s.h5;
  if (week) drawRings(g_dispH5, L < 1200 ? 100 + (g_dispD7 - 100) * easeInOut(L / 1200.0f) : g_dispD7, s.pace5, s.pace7);
  else drawRings(h5, g_dispD7, s.pace5, s.pace7);
  bool on = L > 1100;
  if (on) confettiRain(now);
  float hop = on ? fabsf(sinf((L - 1100) / 260.0f)) * 10 : 0;
  Face f;
  f.y = 108 - hop; f.r = 40; f.col = usageColor(week ? s.h5 : h5); f.look(0, 0);
  if (on) { f.eyes(EYE_HAPPY); f.mouth = M_GRIN; f.blush = true; } else { f.wide = true; f.mouth = M_O; }
  drawFace(f, now);
  partyHat(f);
  label(week ? "Neue Woche!" : (on ? "Frische Tokens!" : "Reset"), C_GREEN);
}
void emoteWorried(const Snapshot& s, uint32_t now) {
  ringsNormal(s);
  Face f;
  f.col = eyeColor(s); f.look((now / 700) % 2 ? 0.7f : -0.7f, 0.1f);
  f.brows = BROW_WORRIED; f.mouth = M_WOBBLE;
  Geo g = drawFace(f, now);
  drop(g.cx + g.r * 0.95f, g.cy - g.r * 0.55f + fmodf(now / 1100.0f, 1.0f) * 12, 0.85f, C_SKY);
  usageLine(s);
}
void scenePanic(const Snapshot& s, uint32_t now) {
  bool fl = (now / 250) % 2;
  ringsNormal(s, fl ? mix(C_TRACK, C_RED, 0.35f) : C_TRACK);
  Face f;
  f.x = CX + sinf(now / 30.0f) * 2.2f; f.col = eyeColor(s); f.wide = true;
  f.look(sinf(now / 90.0f) * 0.3f, 0); f.mouth = M_O; f.mouthAmt = 1.35f;
  drawFace(f, now);
  for (int sd = -1; sd <= 1; sd += 2) {
    float ph = fmodf(now / 700.0f + (sd > 0 ? 0.5f : 0), 1.0f);
    drop(CX + sd * (46 + ph * 26), 70 - ph * 10 + ph * ph * 40, 0.7f * (1 - ph * 0.4f), mix(C_SKY, C_BG, ph * 0.7f));
  }
  text("!", CX + 37, 104 - 35, &fonts::DejaVu24, fl ? C_AMBER : C_RED);
  char buf[32], d[16];
  if (s.lim5 >= 0) { fmtDur(s.lim5, d, sizeof(d)); snprintf(buf, sizeof(buf), "Limit in ~%s", d); }
  else snprintf(buf, sizeof(buf), "%d%%", (int)roundf(s.h5));
  fitText(buf, 182, fl ? C_RED : C_AMBER);
}
void sceneKO(const Snapshot& s, uint32_t now) {
  ringsNormal(s);
  Face f;
  f.y = 110; f.r = 42; f.col = usageColor(100); f.eyes(EYE_SADC); f.mouth = M_FROWN; f.look(0, 0); f.turn = 0;
  Geo g = drawFace(f, now);
  float ph = fmodf(now / 2200.0f, 1.0f);
  if (ph < 0.8f) {
    float q = ph / 0.8f;
    drop(g.fcx - g.ex + g.ew * 0.6f, g.ey + 6 + q * 30, 0.55f, mix(C_SKY, C_BG, fmaxf(0, q - 0.6f) / 0.4f * 0.8f));
  }
  char buf[32];
  int m = g_state.localMinute();
  if (m >= 0 && s.r5 >= 0) {
    int t = (m + (s.r5 + 59) / 60) % 1440;
    snprintf(buf, sizeof(buf), "Pause bis %d:%02d", t / 60, t % 60);
  } else {
    char d[16];
    fmtDur(s.r5, d, sizeof(d));
    snprintf(buf, sizeof(buf), "Pause: %s", d);
  }
  fitText(buf, 182, C_TEXT);
  text("Limit erreicht", CX, 202, &fonts::DejaVu12, C_RED);
}
void emoteLove(const Snapshot& s, uint32_t now) {
  ringsNormal(s);
  Face f;
  f.y = 106; f.col = eyeColor(s); f.eyes(EYE_HEART); f.mouth = M_CAT; f.blush = true; f.look(0, 0); f.turn = 0;
  drawFace(f, now);
  for (int i = 0; i < 3; i++) {
    float ph = fmodf(now / 2400.0f + i / 3.0f, 1.0f);
    heart(CX + (i - 1) * 36 + sinf(ph * 6 + i) * 5, 70 - ph * 26, 4 + 2 * (1 - ph), mix(C_PINK, C_BG, ph));
  }
  usageLine(s);
}
void emoteWink(const Snapshot& s, uint32_t now, uint32_t L) {
  ringsNormal(s);
  bool w = L > 200 && L < 1400;
  Face f;
  f.col = eyeColor(s); f.look(0, 0); f.blink = !w;
  if (w) { f.eyeR = EYE_HAPPY; f.mouth = M_SMIRK; }
  Geo g = drawFace(f, now);
  if (w) sparkle(g.fcx + g.ex + 18, g.ey - 18, 5 + 2 * sinf(now / 80.0f), C_GOLD);
  usageLine(s);
}

// ---- Leerlauf ----
void sceneIdle(const Snapshot& s, uint32_t now, int stage) {
  ringsNormal(s);
  Face f;
  f.col = eyeColor(s);
  float br = sinf(now / 3200.0f * TWO_PI);
  f.y = 104 + br * 1.5f;
  // Stimmung nach Verbrauch
  if (s.h5 >= 80) { f.brows = BROW_WORRIED; f.mouth = M_FLAT; }

  if (g_wakeAt && now - g_wakeAt < 1400) {   // gerade aufgewacht
    f.y -= 6 * sinf((now - g_wakeAt) / 1400.0f * PI);
    f.wide = true; f.mouth = M_O; f.mouthAmt = 0.9f; f.blink = false; f.look(0, -0.1f);
    drawFace(f, now);
    text("!", CX + 37, 104 - 33, &fonts::DejaVu24, C_AMBER);
    usageLine(s);
    return;
  }

  if (stage == IDLE_TIRED) {   // nickt ein und schreckt hoch
    uint32_t L = now % 5200;
    float dy = 0, droop = 0.6f;
    bool sur = false;
    if (L < 3000) { float q = L / 3000.0f; dy = 8 * easeInOut(q); droop = 0.6f + 0.38f * q; }
    else if (L < 3250) { dy = 8 + (-6 - 8) * easeOut(seg(L, 3000, 3250)); droop = 0; sur = true; }
    else if (L < 3900) { dy = -6 + 6 * seg(L, 3250, 3900); droop = 0; sur = true; }
    else droop = 0.6f * seg(L, 3900, 5200);
    f.y = 106 + dy; f.droop = droop; f.wide = sur; f.turn = 0.5f;
    f.look(0, sur ? -0.2f : 0.6f);
    f.mouth = sur ? M_O : M_FLAT; f.mouthAmt = 0.7f; f.blink = !sur && droop < 0.9f;
    drawFace(f, now);
    if (sur) text("!", CX + 36, 106 - 32, &fonts::DejaVu24, C_AMBER);
    usageLine(s);
    return;
  }

  float t = (now - g_animStart) / (float)animLen(g_anim);
  float env = sinf(c01(t) * PI);
  switch (g_anim) {
    case ANIM_LOOK: f.look((t < 0.5f ? -1.0f : 1.0f) * fminf(1.0f, env * 2.5f), 0); break;
    case ANIM_WHISTLE: f.mouth = M_WHISTLE; f.look(0.4f, 0); break;
    case ANIM_HOP: f.y -= fabsf(sinf(t * PI * 3)) * 12 * env; break;
    case ANIM_YAWN: f.yawn = env; f.droop = env * 0.3f; f.mouth = M_YAWN; f.blink = env < 0.2f; break;
    case ANIM_READ: {
      uint32_t L = now - g_animStart;
      if (L > 700 && L < animLen(g_anim) - 700) {
        float q = fmodf(L / 1700.0f, 1.0f);
        float lx = q < 0.85f ? -0.75f + 1.5f * q / 0.85f : 0.75f - 1.5f * (q - 0.85f) / 0.15f;
        f.y = 98; f.r = 38; f.look(lx, 0.75f); f.eyeH = 0.9f; f.mouth = M_SMALL; f.turn = 0.35f;
        drawFace(f, now);
        book(now, CX, 142);
        usageLine(s);
        return;
      }
      break;
    }
    case ANIM_PEEK: {   // taucht ab, linst von unten rein, springt wieder hoch
      uint32_t L = now - g_animStart;
      float y = f.y;
      bool lab = false;
      if (L < 600) y = 104 + (262 - 104) * easeIn(L / 600.0f);
      else if (L < 1400) y = 262;
      else if (L < 1900) y = 262 + (206 - 262) * easeOut(seg(L, 1400, 1900));
      else if (L < 4200) { y = 206; f.look(L < 2600 ? -0.9f : L < 3300 ? 0.9f : 0, -0.3f); }
      else if (L < 4600) y = 206 + (262 - 206) * easeIn(seg(L, 4200, 4600));
      else if (L < 5300) { y = 262 + (104 - 262) * easeBack(seg(L, 4600, 5300)); f.eyes(EYE_HAPPY); f.mouth = M_GRIN; }
      else { lab = true; if (L < 6200) { f.eyes(EYE_HAPPY); f.mouth = M_GRIN; } }
      f.y = y;
      if (y < 250) drawFace(f, now);
      if (lab) usageLine(s);
      return;
    }
    default: break;
  }
  drawFace(f, now);
  usageLine(s);
}

void viewBuddy(const Snapshot& s, uint32_t now, int stage, Cur c, bool& ownStatus) {
  // Prioritaet: Erlaubnis > Fehler > Fertig > Hallo/Tschuess > Emote > Arbeit > Warten > Stimmung > Leerlauf
  ownStatus = true;
  switch (c.sh) {
    case SH_PERM: scenePerm(s, now); return;
    case SH_ERR: sceneError(s, now, c.el); return;
    case SH_DONE: sceneDone(s, now, c.el); return;
    case SH_HELLO: sceneHello(s, now, c.el); return;
    case SH_BYE: sceneHello(s, now, 2900 + c.el); return;
    default: break;
  }
  if (g_winkAt && (int32_t)(now - g_winkAt) >= 0) { startEmote(EM_WINK, now); g_winkAt = 0; }
  if (g_emote != EM_NONE && now - g_emoteAt < emoteLen(g_emote)) {
    uint32_t L = now - g_emoteAt;
    ownStatus = false;
    switch (g_emote) {
      case EM_PARTY: emoteParty(s, now, L, false); return;
      case EM_WEEK: emoteParty(s, now, L, true); return;
      case EM_WORRIED: emoteWorried(s, now); return;
      case EM_PANIC: scenePanic(s, now); return;
      case EM_LOVE: emoteLove(s, now); return;
      case EM_WINK: emoteWink(s, now, L); return;
      default: break;
    }
  }
  g_emote = EM_NONE;
  switch (c.sh) {
    case SH_THINK: case SH_TOOL: sceneThink(s, now, c.el); return;
    case SH_SUB: sceneSub(s, now, c.el); return;
    case SH_COMPACT: sceneCompact(s, now); return;
    case SH_INPUT: if (stage <= IDLE_BORED) { sceneInput(s, now, c.el); return; } break;
    default: break;
  }
  ownStatus = false;
  if (s.h5 >= 99.5f) { sceneKO(s, now); ownStatus = true; return; }
  if (s.lim5 >= 0 || s.h5 >= 90) { scenePanic(s, now); return; }
  sceneIdle(s, now, stage);
}

// ---- Gauge / Details: Aktivitaet als Komet + Status unten ----------------
struct Overlay {
  char txt[24] = {0};
  uint32_t col = C_TEXT, track1 = C_TRACK;
  float comet = 0;
  bool dots = false;
};
Overlay overlayFor(Cur c, uint32_t now) {
  Overlay o;
  switch (c.sh) {
    case SH_THINK: case SH_TOOL: o.comet = 75; o.dots = true; strcpy(o.txt, "DENKT"); break;
    case SH_SUB: o.comet = 120; snprintf(o.txt, sizeof(o.txt), "HELFER x%d", max(1, (int)g_state.act.subs)); break;
    case SH_COMPACT: o.comet = 90; strcpy(o.txt, "RAEUMT AUF"); break;
    case SH_PERM: o.track1 = mix(C_TRACK, C_AMBER, 0.18f + 0.42f * (0.5f + 0.5f * sinf(now / 600.0f * PI)));
                  strcpy(o.txt, "DU BIST DRAN"); o.col = C_AMBER; break;
    case SH_INPUT: strcpy(o.txt, "DU BIST DRAN"); o.col = C_DIM; break;
    case SH_DONE: o.track1 = mix(C_TRACK, C_GREEN, 0.55f * bell(c.el, 0, 600)); strcpy(o.txt, "FERTIG"); o.col = C_GREEN; break;
    case SH_ERR: if (c.el < 600 && (c.el / 100) % 2) o.track1 = mix(C_TRACK, C_RED, 0.6f);
                 strcpy(o.txt, "FEHLER"); o.col = C_RED; break;
    case SH_HELLO: strcpy(o.txt, "HALLO"); break;
    case SH_BYE: strcpy(o.txt, "TSCHUESS"); break;
    default: break;
  }
  return o;
}
void drawOverlayStatus(const Overlay& o, uint32_t now) {
  if (o.dots) {
    int w = textW(o.txt, &fonts::DejaVu12), x0 = CX - 8;
    text(o.txt, x0, 208, &fonts::DejaVu12, o.col);
    int n = (now / 350) % 4;
    for (int i = 0; i < 3; i++) g_cv->fillCircle(x0 + w / 2 + 4 + i * 5, 211, 1, i < n ? o.col : mix(o.col, C_BG, 0.72f));
  } else {
    text(o.txt, CX, 208, &fonts::DejaVu12, o.col);
  }
}

void viewGauge(const Snapshot& s, uint32_t now, const Overlay& o) {
  drawRings(g_dispH5, g_dispD7, s.pace5, s.pace7, false, o.track1);
  if (o.comet > 0) comet(now, g_dispH5, o.comet);

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
  if (s.d7 >= 0) snprintf(buf, sizeof(buf), "Woche %d%%", (int)roundf(s.d7));
  else snprintf(buf, sizeof(buf), "Woche --");
  int w = textW(buf, &fonts::DejaVu12);
  g_cv->fillCircle(CX - w / 2 - 8, 178, 3, usageColor(s.d7));
  text(buf, CX + 4, 178, &fonts::DejaVu12, C_TEXT);
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

void viewDetails(const Snapshot& s, const Overlay& o) {
  drawRings(g_dispH5, g_dispD7, s.pace5, s.pace7, true, o.track1);
  detailBlock("SESSION  -  Reset in", s.h5, s.r5, s.lim5, s.pace5, 50);
  detailBlock("WOCHE  -  Reset in", s.d7, s.r7, s.lim7, s.pace7, 118);
  if (o.txt[0]) return;
  char buf[32], d[16];
  uint32_t age = g_state.ageS + g_state.sinceS();
  fmtDur(age, d, sizeof(d));
  snprintf(buf, sizeof(buf), "Stand: vor %s", d);
  text(buf, CX, 188, &fonts::DejaVu9, C_FAINT);
}

// ---- Warte auf PC: sucht mit dem USB-Stecker ----------------------------
void viewWaiting(uint32_t now) {
  float wig = sinf(now / 260.0f) * 1.5f;
  const float cable[6][2] = {{147, 114}, {139, 128}, {148, 146}, {170, 160}, {196, 171}, {216, 180}};
  for (int i = 0; i < 5; i++) g_cv->drawWideLine(cable[i][0], cable[i][1], cable[i + 1][0], cable[i + 1][1], 1.8f, 0x3F4652u);
  drawRings(0, 0, -1, -1);
  Face f;
  f.x = 106; f.y = 104; f.r = 36; f.col = C_SLEEP; f.lookRange = 1.2f; f.mouth = M_HMM; f.turn = 0.8f;
  drawFace(f, now);
  rrect(146 + wig, 108, 16, 10, 3, 0xD1D5DBu);
  rrect(161 + wig, 110, 8, 6, 1.5f, 0x9CA3AFu);
  g_cv->fillRect(I(163 + wig), 112, 4, 2, 0x6B7280u);
  float ph = fmodf(now / 2400.0f, 1.0f);
  text("?", 172, I(86 - ph * 8), &fonts::DejaVu18, mix(C_DIM, C_BG, ph > 0.7f ? (ph - 0.7f) / 0.3f : 0));
  fitText("Warte auf PC", 174, C_DIM);
  text(g_state.linked() ? "keine Limit-Daten" : "App gestartet?", CX, 195, &fonts::DejaVu12, C_FAINT);
}

// ---- Schlaf: Schlafmuetze, Mond, Sterne ----------------------------------
void sleepFace(uint32_t now, float x, float y, float r) {
  float br = sinf(now / 1700.0f * PI);
  Face f;
  f.x = x; f.y = y + br * 1.2f; f.r = r; f.col = C_SLEEP; f.eyes(EYE_CLOSED); f.mouth = M_O;
  f.mouthAmt = 0.55f + 0.25f * (br * 0.5f + 0.5f); f.look(0, 0); f.turn = 0; f.blink = false;
  drawFace(f, now);
  nightcap(f);
  zzz(now, x, y, r);
}
void viewSleep(const Snapshot& s, uint32_t now) {
  drawRings(g_dispH5, g_dispD7, -1, -1, true, C_TRACK, 0.5f);
  moon(70, 70, 11);
  const float st[3][3] = {{58, 106, 0}, {96, 46, 2.1f}, {86, 82, 4.2f}};
  for (auto& p : st) sparkle(p[0], p[1], 1.5f + 2.5f * fmaxf(0, sinf(now / 700.0f + p[2])), C_MOON);
  sleepFace(now, CX, 112, 40);
  char buf[32];
  if (s.h5 >= 0 && s.d7 >= 0) snprintf(buf, sizeof(buf), "%d%%  -  Woche %d%%", (int)roundf(s.h5), (int)roundf(s.d7));
  else if (s.h5 >= 0) snprintf(buf, sizeof(buf), "%d%%", (int)roundf(s.h5));
  else buf[0] = 0;
  if (buf[0]) text(buf, CX, 176, &fonts::DejaVu12, C_DIM);
  text("Taste = aufwecken", CX, 194, &fonts::DejaVu9, C_FAINT);
}
// Nachts: grosse Uhr, kleiner schlafender Orb
void viewNight(uint32_t now) {
  drawRings(g_dispH5, g_dispD7, -1, -1, true, C_TRACK, 0.75f);
  int m = g_state.localMinute();
  char buf[24];
  snprintf(buf, sizeof(buf), "%d:%02d", m / 60, m % 60);
  text(buf, CX, 64, &fonts::DejaVu24, 0x9AA3B0u);
  sleepFace(now, CX, 128, 30);
  snprintf(buf, sizeof(buf), "Nachtruhe bis %d:%02d", NIGHT_TO_MIN / 60, NIGHT_TO_MIN % 60);
  text(buf, CX, 184, &fonts::DejaVu12, C_FAINT);
}

void drawToast(uint32_t now) {
  if (!g_toast[0] || now > g_toastUntil) return;
  int w = textW(g_toast, &fonts::DejaVu12) + 24;
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
    Face fc;
    fc.y = 98; fc.r = 34; fc.col = i < 18 ? C_SLEEP : C_GREEN; fc.look(0, 0); fc.blink = false;
    if (i < 18) { fc.eyes(EYE_CLOSED); fc.mouth = M_SMALL; }
    else if (i < 22) { fc.wide = true; fc.mouth = M_O; fc.mouthAmt = 0.7f; }
    drawFace(fc, now);
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
  if (!waiting) checkEmotes(s, now);
  int stage = idleStage();
  updateIdle(waiting ? IDLE_ACTIVE : stage, now);
  Cur c = curActivity(now);
  if (g_winkAt && g_view != VIEW_BUDDY && (int32_t)(now - g_winkAt) > 0) g_winkAt = 0;
  bool status = true;
  if (waiting) {
    viewWaiting(now);
  } else if (stage == IDLE_ASLEEP) {
    if (isNight()) { viewNight(now); status = false; }
    else viewSleep(s, now);
  } else if (g_view == VIEW_BUDDY) {
    bool own;
    viewBuddy(s, now, stage, c, own);
    status = !own;
  } else {
    Overlay o = overlayFor(c, now);
    if (g_view == VIEW_DETAILS) viewDetails(s, o);
    else viewGauge(s, now, o);
    if (o.txt[0]) { drawOverlayStatus(o, now); status = false; }
  }
  if (status) drawStatusBar();
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

LedPattern uiLedPattern(uint32_t& col, uint32_t& elapsed) {
  bool alarm, idle;
  col = uiStatusColor(alarm, idle);
  elapsed = 0;
  if (idle) return LEDP_SLEEP;
  Cur c = curActivity(millis());
  switch (c.sh) {
    case SH_PERM: col = C_AMBER; return LEDP_PERM;
    case SH_DONE: elapsed = c.el; return LEDP_DONE;
    case SH_ERR: elapsed = c.el; return LEDP_ERR;
    case SH_TOOL: return LEDP_TOOL;
    case SH_THINK: case SH_SUB: case SH_COMPACT: return LEDP_THINK;
    default: break;
  }
  return alarm ? LEDP_ALARM : LEDP_IDLE;
}

// "A <zustand> [arg]" von der App
void uiActivity(const char* st, const char* arg) {
  Activity& a = g_state.act;
  uint32_t now = millis();
  while (arg && *arg == ' ') arg++;
  // Anfuehrungszeichen entfernen, erstes Wort reicht
  char det[sizeof(a.detail)] = {0};
  if (arg) {
    const char* p = arg;
    if (*p == '"') p++;
    size_t i = 0;
    while (*p && *p != '"' && *p != ' ' && i < sizeof(det) - 1) det[i++] = *p++;
  }
  Act prev = a.act;
  bool wasWorking = a.working();
  Act next = prev;
  if (!strcmp(st, "idle")) next = Act::Idle;
  else if (!strcmp(st, "think")) next = Act::Think;
  else if (!strcmp(st, "tool")) next = Act::Tool;
  else if (!strcmp(st, "sub")) { int n = atoi(det); a.subs = n > 0 ? n : 0; next = n > 0 ? Act::Sub : Act::Think; }
  else if (!strcmp(st, "perm")) { next = Act::Perm; strlcpy(a.detail, det, sizeof(a.detail)); }
  else if (!strcmp(st, "input")) next = Act::Input;
  else if (!strcmp(st, "compact")) next = Act::Compact;
  else if (!strcmp(st, "done") || !strcmp(st, "err") || !strcmp(st, "hello") || !strcmp(st, "bye")) {
    ActEvt e = st[0] == 'd' ? ActEvt::Done : st[0] == 'e' ? ActEvt::Err : st[0] == 'h' ? ActEvt::Hello : ActEvt::Bye;
    a.evt = e;
    a.evtMs = now;
    if (e == ActEvt::Err) strlcpy(a.detail, det, sizeof(a.detail));
    if (e == ActEvt::Done && esp_random() % 100 < 30) g_winkAt = now + DONE_MS + 150;   // ab und zu zwinkern
    if (e != ActEvt::Hello) next = Act::Idle;
    g_state.touch();
  } else return;

  a.refreshMs = now;
  if (next != prev) {
    bool nowWorking = next == Act::Think || next == Act::Tool || next == Act::Sub || next == Act::Compact;
    if (!(wasWorking && nowWorking)) a.sinceMs = now;   // Denken -> Werkzeug -> Denken zaehlt als ein Stueck
    a.act = next;
    g_state.touch();
  } else if (a.working() || a.act == Act::Perm) {
    g_state.touch();   // arbeitet weiter -> nicht einschlafen
  }
  if (a.working() && (a.evt == ActEvt::Done || a.evt == ActEvt::Err)) a.evt = ActEvt::None;   // neue Runde
}

void uiEmote(const char* name) {
  uint32_t now = millis();
  if (!strcmp(name, "party")) startEmote(EM_PARTY, now);
  else if (!strcmp(name, "week")) startEmote(EM_WEEK, now);
  else if (!strcmp(name, "worried")) startEmote(EM_WORRIED, now);
  else if (!strcmp(name, "panic")) startEmote(EM_PANIC, now);
  else if (!strcmp(name, "love")) startEmote(EM_LOVE, now);
  else if (!strcmp(name, "wink")) startEmote(EM_WINK, now);
  else return;
  g_state.touch();
}

#ifdef UI_PREVIEW
// nur fuer die Desktop-Vorschau: Leerlauf-Animation erzwingen
void uiPreviewAnim(int a) {
  g_anim = (IdleAnim)a;
  g_animStart = millis();
  g_animNext = millis() + 600000;
}
#endif
