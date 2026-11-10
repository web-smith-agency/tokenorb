#!/bin/bash
# Baut ein komplettes Release fuer GitHub:
#   dist/release/<version>/
#     TokenOrb-Setup-<v>.exe                  Windows-Installer
#     TokenOrb-Mac-<v>.zip                    Mac-App (universal, ad-hoc signiert)
#     TokenOrb-ESP32-*-update.bin             Firmware fuer das Update aus der App
#     TokenOrb-ESP32-*.bin                    Firmware zum Erst-Flashen (Web-Flasher, Adresse 0x0)
#     manifest.json                           Versionen + SHA-256 (liest die App)
#     SHA256SUMS.txt
#
# Voraussetzungen: Go >= 1.23, makensis (NSIS 3), Python 3, PlatformIO (fuer die Firmware),
# rcodesign (Mac-Signatur, Pfad per RCODESIGN).
set -e
ROOT=$(cd "$(dirname "$0")/.." && pwd)
V=$(sed -n 's/^const appVersion = "\(.*\)"/\1/p' "$ROOT/app/config.go")
FW=$(sed -n 's/^#define FW_VERSION *"\(.*\)"/\1/p' "$ROOT/firmware/include/config.h")
OUT="$ROOT/dist/release/$V"
RCODESIGN=${RCODESIGN:-$(command -v rcodesign || true)}
rm -rf "$OUT"; mkdir -p "$OUT"
echo "App $V, Firmware $FW -> $OUT"

# --- Firmware ------------------------------------------------------------------
if [ "${SKIP_FW:-0}" != 1 ]; then
  (cd "$ROOT/firmware" && for e in c3 s3 wroom; do pio run -e $e >/dev/null; done)
  bash "$ROOT/tools/merge-firmware.sh" >/dev/null 2>&1 || true
fi
for pair in "c3 ESP32-C3-SuperMini" "s3 ESP32-S3-N16R8" "wroom ESP32-WROOM"; do
  set -- $pair
  cp "$ROOT/firmware/.pio/build/$1/firmware.bin" "$OUT/TokenOrb-$2-update.bin"
  cp "$ROOT/dist/TokenOrb-$2.bin" "$OUT/TokenOrb-$2.bin"
done

# --- Windows -------------------------------------------------------------------
cd "$ROOT/app"
export CGO_ENABLED=0
[ -d vendor ] && export GOFLAGS=-mod=vendor
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-H windowsgui -s -w" -o "$ROOT/dist/TokenOrb.exe" .
(cd "$ROOT/installer" && makensis -V2 -DVERSION="$V" -DEXE="$ROOT/dist/TokenOrb.exe" \
   -DOUTFILE="$OUT/TokenOrb-Setup-$V.exe" TokenOrb.nsi)

# --- Mac -----------------------------------------------------------------------
TMP=$(mktemp -d)
GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o "$TMP/arm64" .
GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o "$TMP/amd64" .
APP="$TMP/TokenOrb.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
python3 "$ROOT/tools/lipo.py" "$APP/Contents/MacOS/TokenOrb" "$TMP/arm64" "$TMP/amd64" >/dev/null
chmod +x "$APP/Contents/MacOS/TokenOrb"
cp "$ROOT/installer/mac/AppIcon.icns" "$APP/Contents/Resources/"
BUILD=$(echo "$V" | tr -d .)
sed -e "s#<key>CFBundleShortVersionString</key><string>[^<]*</string>#<key>CFBundleShortVersionString</key><string>$V</string>#" \
    -e "s#<key>CFBundleVersion</key><string>[^<]*</string>#<key>CFBundleVersion</key><string>$BUILD</string>#" \
    "$ROOT/installer/mac/Info.plist" > "$APP/Contents/Info.plist"
if [ -n "$RCODESIGN" ]; then
  "$RCODESIGN" sign "$APP" >/dev/null 2>&1 && echo "Mac-App ad-hoc signiert"
else
  echo "WARNUNG: rcodesign fehlt – Mac-App ist unsigniert" >&2
fi
cp "$ROOT/installer/LIESMICH-Mac.txt" "$TMP/LIESMICH.txt"
(cd "$TMP" && zip -qry "$OUT/TokenOrb-Mac-$V.zip" TokenOrb.app LIESMICH.txt)
rm -rf "$TMP"

# --- manifest.json -------------------------------------------------------------
cd "$OUT"
NOTES_FILE="$ROOT/installer/notes/$V.md"
python3 - "$V" "$FW" "$NOTES_FILE" <<'EOF'
import hashlib, json, os, sys
v, fw, notes_file = sys.argv[1:4]
notes = open(notes_file).read().strip() if os.path.exists(notes_file) else ""
files = {}
for n in sorted(os.listdir(".")):
    if n in ("manifest.json", "SHA256SUMS.txt"):
        continue
    b = open(n, "rb").read()
    files[n] = {"sha256": hashlib.sha256(b).hexdigest(), "size": len(b)}
json.dump({"app": v, "firmware": fw, "notes": notes, "files": files}, open("manifest.json", "w"), indent=1, ensure_ascii=False)
with open("SHA256SUMS.txt", "w") as f:
    for n, x in files.items():
        f.write(f"{x['sha256']}  {n}\n")
EOF
echo "fertig:"; ls -la "$OUT"
