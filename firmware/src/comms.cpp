#include "comms.h"
#include "config.h"
#include "state.h"
#include "ui.h"
#include <Update.h>

#if USE_BLE
#include <NimBLEDevice.h>
#endif

// ---------------------------------------------------------------------------
// Zeilenpuffer je Quelle
// ---------------------------------------------------------------------------
namespace {

struct LineBuf {
  char buf[128];
  size_t len = 0;
  bool overflow = false;
  // gibt true zurueck, wenn eine vollstaendige Zeile vorliegt
  bool feed(char c) {
    if (c == '\r') return false;
    if (c == '\n') {
      buf[len] = 0;
      bool ok = !overflow && len > 0;
      len = 0;
      overflow = false;
      return ok;
    }
    if (len < sizeof(buf) - 1) buf[len++] = c;
    else overflow = true;
    return false;
  }
};

enum Source : uint8_t { SRC_USB = 0, SRC_UART = 1, SRC_BLE = 2 };

// ---- Firmware-Update ueber USB --------------------------------------------
constexpr size_t OTA_BLOCK = 4096;      // so viel schickt der PC pro Bestaetigung
struct Ota {
  bool active = false;
  uint8_t src = 0;
  size_t size = 0, got = 0, acked = 0;
  uint32_t lastMs = 0;
} g_ota;

Stream& otaStream(uint8_t src);
void sendTo(uint8_t src, const char* line);

void otaFail(const char* why) {
  Update.abort();
  char out[64];
  snprintf(out, sizeof(out), "F ERR %s", why);
  sendTo(g_ota.src, out);
  g_ota.active = false;
  g_state.otaPct = -1;
}

void otaStart(uint8_t src, char*& save) {
  char* sz = strtok_r(nullptr, " ", &save);
  char* md5 = strtok_r(nullptr, " ", &save);
  g_ota.src = src;
  if (src == SRC_BLE) { sendTo(src, "F ERR nur-usb"); return; }
  long n = sz ? atol(sz) : 0;
  if (n <= 0) { sendTo(src, "F ERR groesse"); return; }
  if (!Update.begin((size_t)n, U_FLASH)) { sendTo(src, "F ERR platz"); return; }
  if (md5 && strlen(md5) == 32) Update.setMD5(md5);
  g_ota.active = true;
  g_ota.size = (size_t)n;
  g_ota.got = g_ota.acked = 0;
  g_ota.lastMs = millis();
  g_state.otaPct = 0;
  sendTo(src, "F OK");
}

// liest rohe Bytes, solange ein Update laeuft
void otaLoop() {
  Stream& in = otaStream(g_ota.src);
  static uint8_t buf[512];
  while (g_ota.active && in.available()) {
    size_t want = g_ota.size - g_ota.got;
    size_t n = in.readBytes(buf, min(want, min((size_t)in.available(), sizeof(buf))));
    if (n == 0) break;
    if (Update.write(buf, n) != n) { otaFail("schreiben"); return; }
    g_ota.got += n;
    g_ota.lastMs = millis();
    g_state.otaPct = (int)(g_ota.got * 100 / g_ota.size);
    if (g_ota.got == g_ota.size) {
      if (!Update.end(true)) { otaFail(Update.getError() == UPDATE_ERROR_MD5 ? "md5" : "pruefung"); return; }
      sendTo(g_ota.src, "F DONE");
      g_state.otaPct = 100;
      delay(400);
      ESP.restart();
    }
    if (g_ota.got - g_ota.acked >= OTA_BLOCK) {
      g_ota.acked = g_ota.got - (g_ota.got % OTA_BLOCK);
      char out[24];
      snprintf(out, sizeof(out), "F %u", (unsigned)g_ota.acked);
      sendTo(g_ota.src, out);
    }
  }
  if (g_ota.active && millis() - g_ota.lastMs > 8000) otaFail("timeout");
}

LineBuf g_usbBuf;
#if HAS_SERIAL0 && ARDUINO_USB_CDC_ON_BOOT
LineBuf g_uartBuf;
#endif
uint8_t g_lastSerialSrc = 0xFF;  // welcher serielle Port zuletzt gesprochen hat

#if USE_BLE
// Nordic-UART-Service (NUS) – damit kann man auch mit Handy-Apps wie
// "nRF Connect" oder "Serial Bluetooth Terminal" testen.
const char* NUS_SVC = "6E400001-B5A3-F393-E0A9-E50E24DCCA9E";
const char* NUS_RX  = "6E400002-B5A3-F393-E0A9-E50E24DCCA9E";
const char* NUS_TX  = "6E400003-B5A3-F393-E0A9-E50E24DCCA9E";

NimBLECharacteristic* g_tx = nullptr;
volatile bool g_bleConnected = false;

// BLE-Callbacks laufen in einem anderen Task -> Ringpuffer mit Lock
portMUX_TYPE g_bleMux = portMUX_INITIALIZER_UNLOCKED;
char g_bleRing[512];
volatile size_t g_bleHead = 0, g_bleTail = 0;
LineBuf g_bleBuf;

void blePush(const uint8_t* data, size_t n) {
  portENTER_CRITICAL(&g_bleMux);
  for (size_t i = 0; i < n; i++) {
    size_t next = (g_bleHead + 1) % sizeof(g_bleRing);
    if (next == g_bleTail) break;  // voll
    g_bleRing[g_bleHead] = (char)data[i];
    g_bleHead = next;
  }
  // Schreibvorgang ohne Zeilenende als eigene Zeile behandeln
  if (n > 0 && data[n - 1] != '\n') {
    size_t next = (g_bleHead + 1) % sizeof(g_bleRing);
    if (next != g_bleTail) {
      g_bleRing[g_bleHead] = '\n';
      g_bleHead = next;
    }
  }
  portEXIT_CRITICAL(&g_bleMux);
}

bool blePop(char& c) {
  bool ok = false;
  portENTER_CRITICAL(&g_bleMux);
  if (g_bleTail != g_bleHead) {
    c = g_bleRing[g_bleTail];
    g_bleTail = (g_bleTail + 1) % sizeof(g_bleRing);
    ok = true;
  }
  portEXIT_CRITICAL(&g_bleMux);
  return ok;
}

class ServerCb : public NimBLEServerCallbacks {
  void onConnect(NimBLEServer* s, NimBLEConnInfo& info) override {
    g_bleConnected = true;
    // schnelleres Verbindungsintervall anfragen
    s->updateConnParams(info.getConnHandle(), 24, 48, 0, 400);
  }
  void onDisconnect(NimBLEServer* s, NimBLEConnInfo& info, int reason) override {
    g_bleConnected = false;
    NimBLEDevice::startAdvertising();
  }
};

class RxCb : public NimBLECharacteristicCallbacks {
  void onWrite(NimBLECharacteristic* c, NimBLEConnInfo& info) override {
    NimBLEAttValue v = c->getValue();
    blePush(v.data(), v.length());
  }
};
#endif

// ---------------------------------------------------------------------------
// Senden
// ---------------------------------------------------------------------------
void sendTo(uint8_t src, const char* line) {
  switch (src) {
    case SRC_USB:
      Serial.print(line);
      Serial.print('\n');
      break;
#if HAS_SERIAL0 && ARDUINO_USB_CDC_ON_BOOT
    case SRC_UART:
      Serial0.print(line);
      Serial0.print('\n');
      break;
#endif
#if USE_BLE
    case SRC_BLE:
      if (g_bleConnected && g_tx) {
        String s(line);
        s += '\n';
        g_tx->setValue((const uint8_t*)s.c_str(), s.length());
        g_tx->notify();
      }
      break;
#endif
    default:
      break;
  }
}

// ---------------------------------------------------------------------------
// Parser
// ---------------------------------------------------------------------------
bool parseNum(const char* tok, float& out) {
  if (!tok) return false;
  if (tok[0] == '-' && tok[1] == 0) { out = -1; return true; }
  char* end = nullptr;
  out = strtof(tok, &end);
  return end != tok;
}

void handleLine(char* line, uint8_t src) {
  // fuehrende Leerzeichen weg
  while (*line == ' ') line++;
  if (!*line) return;

  g_state.lastLinkMs = millis();
  if (g_state.lastLinkMs == 0) g_state.lastLinkMs = 1;
  g_state.transport = (src == SRC_BLE) ? Transport::Ble : Transport::Usb;
  if (src != SRC_BLE) g_lastSerialSrc = src;

  char* save = nullptr;
  char* cmd = strtok_r(line, " ", &save);
  if (!cmd) return;

  switch (cmd[0]) {
    case '?': {
      char out[40];
      snprintf(out, sizeof(out), "ORB %s %s", FW_VERSION, BOARD_NAME);
      sendTo(src, out);
      break;
    }
    case 'U':
    case 'u': {
      float v[5];
      for (int i = 0; i < 5; i++) {
        if (!parseNum(strtok_r(nullptr, " ", &save), v[i])) return;  // kaputte Zeile ignorieren
      }
      float old5 = g_state.h5.pct, old7 = g_state.d7.pct;
      g_state.h5.pct = v[0] < 0 ? -1 : constrain(v[0], 0.0f, 100.0f);
      g_state.h5.remainS = (int32_t)v[1];
      g_state.d7.pct = v[2] < 0 ? -1 : constrain(v[2], 0.0f, 100.0f);
      g_state.d7.remainS = (int32_t)v[3];
      g_state.ageS = v[4] < 0 ? 0 : (uint32_t)v[4];
      g_state.rxMs = millis();
      if (!g_state.hasData || fabsf(old5 - g_state.h5.pct) >= 0.5f || fabsf(old7 - g_state.d7.pct) >= 0.5f)
        g_state.touch();   // es hat sich wirklich was getan -> Maskottchen wird wach
      g_state.hasData = true;
      break;
    }
    case 'P':
    case 'p':
      break;  // nur Lebenszeichen
    case 'B':
    case 'b': {
      float f;
      if (parseNum(strtok_r(nullptr, " ", &save), f)) onCmdBrightness((int)f);
      break;
    }
    case 'V':
    case 'v': {
      float f;
      if (parseNum(strtok_r(nullptr, " ", &save), f)) onCmdView((int)f);
      break;
    }
    case 'F':
    case 'f':
      if (!g_ota.active) otaStart(src, save);
      break;
    case 'D':
    case 'd': {
      float f;
      if (parseNum(strtok_r(nullptr, " ", &save), f)) onCmdDemo(f > 0);
      break;
    }
    case 'A':
    case 'a': {   // Claude-Aktivitaet
      char* st = strtok_r(nullptr, " ", &save);
      if (st) uiActivity(st, save ? save : "");
      break;
    }
    case 'E':
    case 'e': {   // Emote abspielen
      char* name = strtok_r(nullptr, " ", &save);
      if (name) uiEmote(name);
      break;
    }
    case 'T':
    case 't': {   // Uhrzeit: Unix-Sekunden und Abstand zu UTC in Minuten
      char* ts = strtok_r(nullptr, " ", &save);
      char* tz = strtok_r(nullptr, " ", &save);
      if (!ts) break;
      long long t = atoll(ts);
      if (t < 1600000000LL) break;
      g_state.unixAtRx = t;
      g_state.timeRxMs = millis();
      g_state.tzMin = tz ? (int16_t)constrain(atoi(tz), -900, 900) : 0;
      g_state.hasTime = true;
      break;
    }
    default:
      break;
  }
}

Stream& otaStream(uint8_t src) {
#if HAS_SERIAL0 && ARDUINO_USB_CDC_ON_BOOT
  if (src == SRC_UART) return Serial0;
#endif
  (void)src;
  return Serial;
}

}  // namespace

// ---------------------------------------------------------------------------
// Oeffentliche Funktionen
// ---------------------------------------------------------------------------
void commsBegin(const char* bleName) {
  Serial.setRxBufferSize(OTA_BLOCK + 512);   // ein ganzer Update-Block passt in den Puffer
  Serial.begin(115200);
#if ARDUINO_USB_CDC_ON_BOOT && ARDUINO_USB_MODE
  // nicht blockieren, wenn kein PC am nativen USB haengt
  Serial.setTxTimeoutMs(0);
#endif
#if HAS_SERIAL0 && ARDUINO_USB_CDC_ON_BOOT
  Serial0.setRxBufferSize(OTA_BLOCK + 512);
  Serial0.begin(115200);
#endif

#if USE_BLE
  NimBLEDevice::init(bleName);
  NimBLEDevice::setPower(3);
  NimBLEDevice::setMTU(185);
  NimBLEServer* server = NimBLEDevice::createServer();
  server->setCallbacks(new ServerCb());
  NimBLEService* svc = server->createService(NUS_SVC);
  NimBLECharacteristic* rx =
      svc->createCharacteristic(NUS_RX, NIMBLE_PROPERTY::WRITE | NIMBLE_PROPERTY::WRITE_NR);
  rx->setCallbacks(new RxCb());
  g_tx = svc->createCharacteristic(NUS_TX, NIMBLE_PROPERTY::NOTIFY);
  server->start();

  NimBLEAdvertising* adv = NimBLEDevice::getAdvertising();
  NimBLEAdvertisementData advData;
  advData.setFlags(BLE_HS_ADV_F_DISC_GEN | BLE_HS_ADV_F_BREDR_UNSUP);
  advData.addServiceUUID(NimBLEUUID(NUS_SVC));
  adv->setAdvertisementData(advData);
  NimBLEAdvertisementData scanData;
  scanData.setName(bleName);
  adv->setScanResponseData(scanData);
  adv->start();
#else
  (void)bleName;
#endif
}

void commsLoop() {
  if (g_ota.active) { otaLoop(); return; }
  while (Serial.available() && !g_ota.active) {
    if (g_usbBuf.feed((char)Serial.read())) handleLine(g_usbBuf.buf, SRC_USB);
  }
#if HAS_SERIAL0 && ARDUINO_USB_CDC_ON_BOOT
  while (Serial0.available() && !g_ota.active) {
    if (g_uartBuf.feed((char)Serial0.read())) handleLine(g_uartBuf.buf, SRC_UART);
  }
#endif
#if USE_BLE
  char c;
  while (blePop(c)) {
    if (g_bleBuf.feed(c)) handleLine(g_bleBuf.buf, SRC_BLE);
  }
#endif
}

void commsSend(const char* line) {
  if (g_lastSerialSrc != 0xFF) sendTo(g_lastSerialSrc, line);
#if USE_BLE
  if (g_bleConnected) sendTo(SRC_BLE, line);
#endif
}

bool commsBleConnected() {
#if USE_BLE
  return g_bleConnected;
#else
  return false;
#endif
}
