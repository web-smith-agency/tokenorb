# Neues Release veröffentlichen

1. Versionen hochzählen
   - App: `app/config.go` → `appVersion`
   - Firmware (nur wenn sich am Puck was ändert): `firmware/include/config.h` → `FW_VERSION`
2. Änderungen in `installer/notes/<version>.md` notieren (erscheinen in der App unter „Erweitert“).
3. Bauen: `bash tools/build-release.sh` → alles liegt in `dist/release/<version>/`.
4. Auf GitHub (öffentliches Repo): Releases → „Draft a new release“
   - Tag: `v<version>` (z.B. `v1.3.0`), Titel beliebig
   - **alle** Dateien aus `dist/release/<version>/` hochladen, inkl. `manifest.json`
   - „Publish release“ (nicht als Pre-release markieren)
5. Fertig: Die Apps finden das Update beim nächsten Check (Start + alle 6 Stunden)
   oder sofort über „Nach Updates suchen“.

## Dateien im Release

| Datei | Wofür |
|---|---|
| `manifest.json` | Versionen + SHA-256 aller Dateien – ohne sie ignoriert die App das Release |
| `TokenOrb-Setup-<v>.exe` | Windows-Installer (Update läuft still mit `/S`) |
| `TokenOrb-Mac-<v>.zip` | Mac-App |
| `TokenOrb-ESP32-*-update.bin` | Firmware-Update aus der App (per USB) |
| `TokenOrb-ESP32-*.bin` | Firmware zum Erst-Flashen im Browser (Adresse 0x0) |

Die Dateinamen nicht ändern – die App sucht genau nach diesen Namen.

## Hinweise

- Windows-Installer und Mac-App sind nicht mit einem gekauften Zertifikat signiert.
  Windows zeigt beim ersten Start „Der Computer wurde durch Windows geschützt“ →
  „Weitere Informationen“ → „Trotzdem ausführen“. Updates aus der App laufen danach ohne Rückfrage.
- Firmware ab 1.2.0 kann sich per USB selbst aktualisieren. Ältere Firmware einmalig im
  Browser flashen (esptool-js, Datei `TokenOrb-ESP32-<board>.bin` an 0x0) – vorher in der App
  unter „Erweitert“ den USB-Port freigeben.
- Update-Quelle: Konstante `defaultUpdateRepo` in `app/updater.go`; einzelne Installationen
  können sie im Fenster unter „Erweitert“ überschreiben.
