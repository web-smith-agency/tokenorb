#pragma once
// =====================================================================
//  TokenOrb – Hardware-Konfiguration
//  Pins hier anpassen, falls du anders verdrahtest.
// =====================================================================

#define FW_VERSION    "1.3.1"
#define PRODUCT_NAME  "TokenOrb"      // Anzeigename / Bluetooth-Praefix
#define BLE_PREFIX    "Orb-"          // Bluetooth-Name: Orb-XXXX

#ifndef USE_BLE
  #define USE_BLE 1
#endif

#if defined(BOARD_S3)
  // ---- ESP32-S3 N16R8 DevKit -----------------------------------------
  // GPIO 26-37 sind beim N16R8 fuer Flash/PSRAM belegt -> nicht benutzen!
  #define BOARD_NAME     "S3"
  #define PIN_TFT_SCLK   12   // Display SCL
  #define PIN_TFT_MOSI   11   // Display SDA
  #define PIN_TFT_CS     10   // Display CS
  #define PIN_TFT_DC      9   // Display DC
  #define PIN_TFT_RST    14   // Display RES
  #define PIN_TFT_BL     13   // Display BLK (-1 = nicht angeschlossen)
  #define PIN_BTN1        4   // Taste 1 (gegen GND)
  #define PIN_BTN2        5   // Taste 2 (gegen GND)
  #define PIN_LED         6   // WS2812 DIN
  #define PIN_BAT_ADC    -1   // Akku-Spannungsteiler 100k/100k, z.B. GPIO 1 (-1 = aus)
  #define TFT_SPI_HOST   SPI2_HOST
  #define HAS_SERIAL0     1   // zweiter USB-Anschluss ("COM"/UART) wird mitgelesen

#elif defined(BOARD_C3)
  // ---- ESP32-C3 Super Mini -------------------------------------------
  // GPIO 2/8/9 sind Boot-Pins, 8 = Onboard-LED, 9 = BOOT-Taste -> frei lassen
  #define BOARD_NAME     "C3"
  #define PIN_TFT_SCLK    6   // Display SCL
  #define PIN_TFT_MOSI    7   // Display SDA
  #define PIN_TFT_CS     10   // Display CS
  #define PIN_TFT_DC      5   // Display DC
  #define PIN_TFT_RST     3   // Display RES
  #define PIN_TFT_BL      4   // Display BLK
  #define PIN_BTN1        0   // Taste 1 (gegen GND)
  #define PIN_BTN2        1   // Taste 2 (gegen GND)
  #define PIN_LED        20   // WS2812 DIN
  #define PIN_BAT_ADC    -1
  #define TFT_SPI_HOST   SPI2_HOST
  #define HAS_SERIAL0     0

#elif defined(BOARD_WROOM)
  // ---- klassischer ESP32-WROOM-32 DevKit -----------------------------
  #define BOARD_NAME     "WROOM"
  #define PIN_TFT_SCLK   18
  #define PIN_TFT_MOSI   23
  #define PIN_TFT_CS      5
  #define PIN_TFT_DC     16
  #define PIN_TFT_RST    17
  #define PIN_TFT_BL      4
  #define PIN_BTN1       32
  #define PIN_BTN2       33
  #define PIN_LED        25
  #define PIN_BAT_ADC    -1   // z.B. GPIO 34 mit Spannungsteiler
  #define TFT_SPI_HOST   SPI3_HOST
  #define HAS_SERIAL0     0

#else
  #error "Kein Board gewaehlt (BOARD_C3, BOARD_S3 oder BOARD_WROOM)"
#endif

// ---- Display ---------------------------------------------------------
// Falls Farben vertauscht/invertiert aussehen, diese zwei Werte probieren.
#ifndef TFT_INVERT
  #ifdef PANEL_ILI9341
    #define TFT_INVERT   false
  #else
    #define TFT_INVERT   true
  #endif
#endif
#define TFT_RGB_ORDER  false   // true = BGR-Panel
#ifdef SIM_BUILD
  #define TFT_ROTATION 0       // Wokwi-Simulation
#else
  #define TFT_ROTATION 1       // 0..3 (je +90 Grad im Uhrzeigersinn); 1 = Display im Orb-Gehaeuse (Stiftleiste links)
#endif
#define TFT_SPI_FREQ   40000000

// ---- LEDs ------------------------------------------------------------
#define LED_COUNT        2     // beide Mini-WS2812 hintereinander (DOUT -> DIN)
#define LED_MAX_BRIGHT   150   // 0..255 (bis 1.2.0: 60, war zu dunkel)

// ---- Verhalten -------------------------------------------------------
#define LINK_TIMEOUT_MS      45000UL   // so lange ohne Daten -> "Warte auf PC"
#define LONG_PRESS_MS        600
#define COMBO_PRESS_MS       2000      // beide Tasten halten -> Demo-Modus
#define IDLE_DIM_MS          (10UL * 60UL * 1000UL)  // ohne PC nach 10 min dimmen

// Leerlauf-Stimmungen: "nichts passiert" = keine neuen Werte und keine Taste
#define IDLE_BORED_MS        ( 5UL * 60UL * 1000UL)  // gelangweilt: schaut rum, pfeift, huepft
#define IDLE_TIRED_MS        (15UL * 60UL * 1000UL)  // muede: Lider haengen, gaehnt
#define IDLE_SLEEP_MS        (30UL * 60UL * 1000UL)  // schlaeft: Display gedimmt, Taste weckt
#define SLEEP_BRIGHT_PCT     10

// Claude-Aktivitaet: ohne neue A-Zeile so lange -> Leerlauf (App wiederholt alle 15 s)
#define ACT_TIMEOUT_MS       60000UL
#define DOUBLE_PRESS_MS      350       // Taste 1 doppelt = streicheln

// Nachtmodus (braucht die Uhrzeit von der App): frueher einschlafen, Uhr zeigen
#define NIGHT_FROM_MIN       (23 * 60)  // 23:00
#define NIGHT_TO_MIN         ( 7 * 60)  // 07:00
#define NIGHT_SLEEP_MS       ( 5UL * 60UL * 1000UL)

// Firmenlogo beim Start (include/logo.h, erzeugt mit tools/make_logo.py)
#define SHOW_BOOT_LOGO       1
