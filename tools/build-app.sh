#!/bin/bash
# Baut TokenOrb.exe (Windows x64). Laeuft unter Linux, macOS oder Windows (Git Bash) mit Go >= 1.23.
set -e
cd "$(dirname "$0")/../app"
export GOOS=windows GOARCH=amd64 CGO_ENABLED=0
[ -d vendor ] && export GOFLAGS=-mod=vendor
mkdir -p ../dist
go build -trimpath -ldflags "-H windowsgui -s -w" -o ../dist/TokenOrb.exe .
echo "fertig: dist/TokenOrb.exe ($(du -h ../dist/TokenOrb.exe | cut -f1))"
