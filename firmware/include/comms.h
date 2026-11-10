#pragma once
#include <Arduino.h>

// Textprotokoll (zeilenbasiert, identisch ueber USB und Bluetooth)
//
// PC -> Geraet
//   ?                                Handshake, Antwort: "ORB <version> <board>"
//   U <p5> <r5> <p7> <r7> <alter>    Nutzungsdaten: Prozent + Sekunden bis Reset je Fenster,
//                                    Alter der Daten in Sekunden. "-" = unbekannt
//                                    Beispiel: U 23.5 4980 41 302400 12
//   P                                Ping (haelt die Verbindung "lebendig")
//   B <0..100>                       Helligkeit in Prozent
//   V <0..2>                         Ansicht waehlen
//   D <0|1>                          Demo-Modus aus/an
//   F <bytes> <md5>                  Firmware-Update (nur USB/UART, ab FW 1.2.0):
//                                    Antwort "F OK", danach die Binaerdaten in Bloecken
//                                    zu je 4096 Byte; nach jedem Block bestaetigt das
//                                    Geraet mit "F <empfangen>". Am Ende "F DONE" und
//                                    Neustart, bei Fehlern "F ERR <grund>".
//
// Geraet -> PC
//   ORB <version> <board>            Antwort auf "?"
//   K <n>                            Tastenereignis (1 = Taste 2 kurz, 2 = Taste 2 lang)

void commsBegin(const char* bleName);
void commsLoop();
void commsSend(const char* line);    // an den zuletzt aktiven Kanal (USB und/oder BLE)
bool commsBleConnected();

// werden in main.cpp implementiert
void onCmdBrightness(int pct);
void onCmdView(int view);
void onCmdDemo(bool on);
