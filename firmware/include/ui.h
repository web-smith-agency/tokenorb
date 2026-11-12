#pragma once
#include <Arduino.h>
#define LGFX_USE_V1
#include <LovyanGFX.hpp>

enum View : int { VIEW_GAUGE = 0, VIEW_BUDDY = 1, VIEW_DETAILS = 2, VIEW_COUNT = 3 };

// LED-Muster (umgesetzt in main.cpp)
enum LedPattern : uint8_t { LEDP_IDLE, LEDP_ALARM, LEDP_SLEEP, LEDP_THINK, LEDP_TOOL, LEDP_PERM, LEDP_DONE, LEDP_ERR };

void uiBegin(lgfx::LGFX_Device* tft);   // nullptr = nur in den Speicher zeichnen (Desktop-Vorschau)
LGFX_Sprite* uiCanvas();
void uiSplash();
void uiRender();                  // zeichnet einen Frame
void uiToast(const char* msg);    // kurzer Hinweis unten (ca. 1,5 s)
void uiSetView(int v);
int uiGetView();
// fuer die LEDs: aktuelle "Status-Farbe" (0xRRGGBB) und ob gewarnt wird
uint32_t uiStatusColor(bool& alarm, bool& idle);
// welches LED-Muster gerade gilt; col = Grundfarbe, elapsed = ms seit Ereignis (Fertig/Fehler)
LedPattern uiLedPattern(uint32_t& col, uint32_t& elapsed);
// schlaeft das Maskottchen gerade (lange nichts passiert)?
bool uiAsleep();
// Claude-Aktivitaet von der App: "think", "tool bash", "perm Bash", "done", ...
void uiActivity(const char* state, const char* arg);
// Emote abspielen: party, week, worried, panic, love, wink
void uiEmote(const char* name);
// fuer die Desktop-Vorschau: Logo-Bild einzeln zeichnen
void uiDrawLogoFrame(float f);
