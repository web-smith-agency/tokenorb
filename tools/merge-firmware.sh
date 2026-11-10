#!/bin/bash
# Fasst Bootloader, Partitionstabelle und App zu EINER Datei zusammen.
# Die kann man im Browser flashen (Adresse 0x0) – ohne PlatformIO.
set -e
cd "$(dirname "$0")/../firmware"
ESPTOOL=${ESPTOOL:-$HOME/.platformio/penv/bin/esptool}
BOOTAPP=$(find "$HOME/.platformio/packages" -name boot_app0.bin | head -1)
mkdir -p ../dist

merge() { # env chip bootloader-offset flash-mode flash-size ausgabe
  local env=$1 chip=$2 boff=$3 mode=$4 size=$5 out=$6 B=.pio/build/$1
  "$ESPTOOL" --chip "$chip" merge-bin -o "../dist/$out" \
    --flash-mode "$mode" --flash-freq 80m --flash-size "$size" \
    "$boff" "$B/bootloader.bin" 0x8000 "$B/partitions.bin" 0xe000 "$BOOTAPP" 0x10000 "$B/firmware.bin" >/dev/null
  echo "dist/$out"
}

merge c3    esp32c3 0x0    dio  4MB  TokenOrb-ESP32-C3-SuperMini.bin
merge s3    esp32s3 0x0    qio  16MB TokenOrb-ESP32-S3-N16R8.bin
merge wroom esp32   0x1000 dio  4MB  TokenOrb-ESP32-WROOM.bin
merge sim   esp32s3 0x0    dio  8MB  TokenOrb-Wokwi-Simulation.bin
