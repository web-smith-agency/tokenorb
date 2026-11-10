#pragma once
#include <Arduino.h>
#define LGFX_USE_V1
#include <LovyanGFX.hpp>

enum View : int { VIEW_GAUGE = 0, VIEW_BUDDY = 1, VIEW_DETAILS = 2, VIEW_COUNT = 3 };

void uiBegin(lgfx::LGFX_Device* tft);   // nullptr = nur in den Speicher zeichnen (Desktop-Vorschau)
LGFX_Sprite* uiCanvas();
void uiSplash();
void uiRender();                  // zeichnet einen Frame
void uiToast(const char* msg);    // kurzer Hinweis unten (ca. 1,5 s)
void uiSetView(int v);
int uiGetView();
// fuer die LEDs: aktuelle "Status-Farbe" (0xRRGGBB) und ob gewarnt wird
uint32_t uiStatusColor(bool& alarm, bool& idle);
// schlaeft das Maskottchen gerade (lange nichts passiert)?
bool uiAsleep();
// fuer die Desktop-Vorschau: Logo-Bild einzeln zeichnen
void uiDrawLogoFrame(float f);
