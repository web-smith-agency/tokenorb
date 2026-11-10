//go:build darwin

package main

// macOS: USB-Seriell-Geraete heissen /dev/cu.usbmodem* (ESP32-C3/S3 natives USB),
// /dev/cu.usbserial*, /dev/cu.wchusbserial* (CH340) oder /dev/cu.SLAB_USBtoUART (CP210x).

import (
	"errors"
	"path/filepath"
	"time"
)

func serialCandidates(lastPort string) []string {
	var candidates []string
	for _, pat := range []string{"/dev/cu.usbmodem*", "/dev/cu.usbserial*", "/dev/cu.wchusbserial*", "/dev/cu.SLAB_USBtoUART*"} {
		m, _ := filepath.Glob(pat)
		for _, p := range m {
			if p == lastPort {
				candidates = append([]string{p}, candidates...)
			} else {
				candidates = append(candidates, p)
			}
		}
	}
	return candidates
}

// Bluetooth gibt es in der Mac-Version (noch) nicht – nur USB.
var errNoAdapter = errors.New("kein Bluetooth")

func connectBLE(timeout time.Duration) (link, error) { return nil, errNoAdapter }
