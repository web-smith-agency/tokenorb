# TokenOrb

Kleiner Schreibtisch-Puck mit rundem Display, der deine **Claude-Code-Limits** anzeigt: die 5-Stunden-Session und das Wochenlimit, mit Restzeit bis zum Reset. Dazu gibt es eine App für **Windows** und **macOS**, die die Werte an den Puck schickt.

Mehr zum Orb: <https://web-smith.de/orb/>

## Download

Alles liegt unter **[Releases](https://github.com/web-smith-agency/tokenorb/releases/latest)**:

| Datei | Wofür |
|---|---|
| `TokenOrb-Setup-<version>.exe` | Windows 10/11 (64 Bit), Installation ohne Adminrechte |
| `TokenOrb-Mac-<version>.zip` | macOS 11 oder neuer (Apple Silicon und Intel) |
| `TokenOrb-ESP32-<board>.bin` | Firmware zum ersten Flashen im Browser (Adresse `0x0`) |
| `TokenOrb-ESP32-<board>-update.bin` | Firmware-Update, spielt die App selbst auf |

Nach der Installation sucht die App selbst nach neuen Versionen für sich und für den Puck. Installiert wird per Klick im Fenster unter „Updates“.

> Windows meldet beim ersten Start eventuell „Der Computer wurde durch Windows geschützt“, weil der Installer nicht mit einem gekauften Zertifikat signiert ist. Klick auf „Weitere Informationen“ und dann auf „Trotzdem ausführen“. Auf dem Mac geht's über Systemeinstellungen → Datenschutz & Sicherheit → „Dennoch öffnen“.

## Was passiert mit meinen Daten?

Kurz: **Alles bleibt auf deinem Rechner.** Es gibt kein Konto, keine Telemetrie und keine Statistik. Es geht nichts an Web-Smith oder irgendeinen anderen Server von uns. Der Code liegt hier offen, damit du das selbst nachprüfen kannst.

**Was die App liest und speichert**

- **Statuszeile von Claude Code.** Bei der Installation trägt TokenOrb in `~/.claude/settings.json` eine Statuszeile ein. Hatte Claude Code dort schon eine eigene Statuszeile, bleibt sie erhalten und läuft weiter. Claude Code übergibt der Statuszeile ohnehin Daten zur Sitzung. TokenOrb speichert davon nur die **Prozentwerte und Reset-Zeiten der Limits** und den **Modellnamen**. Das landet lokal in `usage.json`, also in `%LOCALAPPDATA%\TokenOrb` bzw. `~/Library/Application Support/TokenOrb`. Prompts, Code, Dateien und Gesprächsinhalte werden nicht gelesen. Beim Deinstallieren wird der Eintrag in `settings.json` wieder entfernt (`app/claudesettings.go`, `app/statusline.go`).
- **Hooks von Claude Code (ab 1.4.0).** Damit der Puck zeigt, ob Claude gerade arbeitet, trägt TokenOrb in derselben `settings.json` ein paar Hooks ein (Prompt abgeschickt, Werkzeug startet/fertig, Rückfrage, Helfer, fertig, Fehler, Sitzung start/ende). Sie laufen im Hintergrund, Claude Code wartet nicht auf TokenOrb. Claude Code übergibt den Hooks Daten zum Ereignis, bei manchen auch den Prompt oder die Eingabe eines Werkzeugs. TokenOrb liest davon nur **Art des Ereignisses, Werkzeugname, Sitzungs-ID, Art der Benachrichtigung bzw. des Fehlers** und speichert daraus pro Sitzung einen kurzen Zustand (z.B. „denkt“, „wartet auf Erlaubnis für Bash“) lokal im Ordner `sitzungen`. Prompts, Befehle, Dateinamen und Code werden weder gelesen noch gespeichert. Eigene Hooks bleiben erhalten, beim Deinstallieren werden nur unsere Einträge entfernt (`app/activity.go`, `app/claudesettings.go`).
- **Konto-Modus.** Der ist **standardmäßig aus**. Wenn du ihn einschaltest, liest die App die Anmeldung von Claude Code lokal aus: `~/.claude/.credentials.json`, auf dem Mac den Schlüsselbund-Eintrag „Claude Code-credentials“. Damit fragt sie alle 3 Minuten **nur** `https://api.anthropic.com/api/oauth/usage` ab. Diese Adresse ist nicht offiziell dokumentiert, das Anmeldetoken geht aber ausschließlich an Anthropic. Ist die Anmeldung abgelaufen, startet die App einmal `claude -p hi --model haiku`, eine winzige Anfrage, damit Claude Code die Anmeldung selbst erneuert (`app/account.go`).
- **Taste 2 am Puck.** Die App drückt die eingestellte Taste, z.B. Shift+Tab, im gerade aktiven Fenster. Sonst nichts (`app/keys_*.go`).

**Welche Verbindungen die App aufbaut**

| Ziel | Wann | Wozu |
|---|---|---|
| `api.github.com`, GitHub-Downloads | beim Start und alle 6 Stunden (abschaltbar) | nach Updates suchen, Updates laden |
| `api.anthropic.com` | nur im Konto-Modus | Limits abfragen |
| `127.0.0.1:47123` | immer, nur auf deinem Rechner | das Einstellungsfenster |

Mehr Verbindungen gibt es nicht. Das Einstellungsfenster ist nur auf deinem eigenen Rechner erreichbar und nimmt keine Anfragen von fremden Webseiten an (`app/webui.go`). Jedes Update wird vor der Installation per SHA-256 gegen die `manifest.json` im Release geprüft (`app/updater.go`).

**Der Puck** hat kein WLAN. Er bekommt per USB oder Bluetooth (Windows) nur die Prozentwerte und Restzeiten, den kurzen Aktivitätszustand (z.B. „denkt“, „fertig“) und die Uhrzeit für den Nachtmodus und meldet Tastendrücke zurück. Das Protokoll ist in `firmware/include/comms.h` beschrieben.

## Selbst bauen

**App** (Go ≥ 1.23, alle Abhängigkeiten liegen in `app/vendor`):

```bash
cd app
GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui" -o TokenOrb.exe .
GOOS=darwin  GOARCH=arm64 go build -o TokenOrb .
go test ./...
```

**Firmware** (PlatformIO):

```bash
cd firmware
pio run -e c3        # ESP32-C3 Super Mini  (außerdem: s3, wroom)
```

**Komplettes Release** mit Windows-Installer (NSIS), Mac-App und Firmware: `bash tools/build-release.sh`, Details in `installer/RELEASE.md`.

## Aufbau

```
app/         Windows-/Mac-App (Go): Statuszeile, Hooks, Verbindung zum Puck, Einstellungsfenster, Updates
firmware/    Puck-Firmware (ESP32, PlatformIO): Anzeige, LEDs, Tasten, USB/Bluetooth, Update über USB
installer/   Windows-Installer (NSIS), Mac-Vorlagen, Release-Notizen
tools/       Build-Skripte
```

Platine und Gehäuse sind nicht Teil dieses Repos.

## Lizenz

TokenOrb steht unter der **[PolyForm Noncommercial License 1.0.0](LICENSE)**. Du darfst den Code lesen, prüfen, verändern und für dich privat oder nicht-kommerziell nutzen, auch einen eigenen Orb nachbauen. Eine kommerzielle Nutzung, etwa der Verkauf von Geräten oder Software auf Basis dieses Codes, ist nicht erlaubt. Für Anfragen: <https://web-smith.de>

Mitgelieferte Bibliotheken haben ihre eigenen Lizenzen:
- in der App unter `app/vendor`: fyne.io/systray (Apache 2.0), go.bug.st/serial, golang.org/x/sys, tinygo bluetooth, godbus (BSD), go-ole, winrt-go, goselect (MIT)
- in der Firmware: LovyanGFX (FreeBSD), Adafruit NeoPixel (LGPL-3.0), NimBLE-Arduino (Apache 2.0)

Die jeweiligen Lizenztexte liegen bei den Bibliotheken.
