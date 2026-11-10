package main

// Firmware-Update ueber das USB-Kabel (Gegenstueck: firmware/src/comms.cpp, Befehl "F").
//
//   PC:     F <bytes> <md5>          Gerät: F OK  (oder F ERR <grund>)
//   PC:     4096 Byte roh            Gerät: F <empfangen gesamt>
//   ...     letzter (Teil-)Block     Gerät: F DONE  -> Neustart mit neuer Firmware

import (
	"crypto/md5"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const otaBlock = 4096

// Wartezeiten (in Tests verkuerzt)
var (
	otaStartTimeout = 15 * time.Second
	otaBlockTimeout = 20 * time.Second
)

// otaPort ist das, was otaFlash von einer Verbindung braucht (serialLink erfuellt es).
type otaPort interface {
	Write(b []byte) (int, error)
	Send(line string) error
	Lines() <-chan string
	Done() <-chan struct{}
}

var errOtaLost = errors.New("Verbindung zum Puck unterbrochen")

// otaCapable: ab Firmware 1.2.0 kann sich der Puck selbst aktualisieren.
func otaCapable(fwVersion string) bool {
	return fwVersion != "" && !versionLess(fwVersion, "1.2.0")
}

// parseDeviceID zerlegt "ORB 1.2.0 C3" in Version und Board.
func parseDeviceID(id string) (version, board string) {
	f := strings.Fields(id)
	if len(f) >= 2 && f[0] == "ORB" {
		version = f[1]
	}
	if len(f) >= 3 {
		board = f[2]
	}
	return
}

// otaWait liest Zeilen, bis match true liefert oder einen Fehler meldet.
func otaWait(p otaPort, timeout time.Duration, match func(line string) (bool, error)) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case line := <-p.Lines():
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "F ERR") {
				return fmt.Errorf("Puck meldet Fehler: %s", strings.TrimSpace(strings.TrimPrefix(line, "F ERR")))
			}
			ok, err := match(line)
			if err != nil || ok {
				return err
			}
		case <-p.Done():
			return errOtaLost
		case <-deadline.C:
			return errors.New("Puck antwortet nicht (Zeitüberschreitung)")
		}
	}
}

func otaFlash(p otaPort, img []byte, progress func(done, total int)) error {
	if len(img) < 1024 {
		return errors.New("Firmware-Datei ist zu klein")
	}
	if img[0] != 0xE9 {
		return errors.New("das ist keine ESP32-App-Firmware (erwartet wird die *-update.bin)")
	}
	sum := md5.Sum(img)
	if err := p.Send(fmt.Sprintf("F %d %x", len(img), sum)); err != nil {
		return errOtaLost
	}
	if err := otaWait(p, otaStartTimeout, func(l string) (bool, error) { return l == "F OK", nil }); err != nil {
		return err
	}
	if progress != nil {
		progress(0, len(img))
	}
	for off := 0; off < len(img); {
		end := off + otaBlock
		if end > len(img) {
			end = len(img)
		}
		if _, err := p.Write(img[off:end]); err != nil {
			return errOtaLost
		}
		last := end == len(img)
		want := end
		err := otaWait(p, otaBlockTimeout, func(l string) (bool, error) {
			if l == "F DONE" {
				if last {
					return true, nil
				}
				return false, errors.New("Puck hat zu früh abgeschlossen")
			}
			if !last && strings.HasPrefix(l, "F ") {
				if n, e := strconv.Atoi(strings.TrimSpace(l[2:])); e == nil && n >= want {
					return true, nil
				}
			}
			return false, nil
		})
		if err != nil {
			if last && errors.Is(err, errOtaLost) {
				return errors.New("Verbindung beim Abschluss getrennt – nach dem Neustart die Version prüfen")
			}
			return err
		}
		off = end
		if progress != nil {
			progress(off, len(img))
		}
	}
	return nil
}
