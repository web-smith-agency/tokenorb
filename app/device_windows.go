//go:build windows

package main

// Windows: USB-Ports per Hersteller-ID finden und Bluetooth LE.

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial/enumerator"
	"tinygo.org/x/bluetooth"
)

// USB-Seriell-Chips, die auf ESP32-Boards verbaut werden. Andere COM-Ports
// (z.B. 3D-Drucker) werden nie angefasst.
var knownVIDs = map[string]bool{
	"303A": true, // Espressif (natives USB bei C3/S3)
	"10C4": true, // Silicon Labs CP210x
	"1A86": true, // WCH CH340/CH343
	"0403": true, // FTDI
}

func serialCandidates(lastPort string) []string {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return nil
	}
	var candidates []string
	for _, p := range ports {
		if p.Name == lastPort && p.IsUSB {
			candidates = append([]string{p.Name}, candidates...)
		} else if p.IsUSB && knownVIDs[strings.ToUpper(p.VID)] {
			candidates = append(candidates, p.Name)
		}
	}
	return candidates
}

// ===========================================================================
// Bluetooth LE (Nordic UART Service)
// ===========================================================================

var (
	nusService = mustUUID("6e400001-b5a3-f393-e0a9-e50e24dcca9e")
	nusRX      = mustUUID("6e400002-b5a3-f393-e0a9-e50e24dcca9e") // wir schreiben
	nusTX      = mustUUID("6e400003-b5a3-f393-e0a9-e50e24dcca9e") // Geraet meldet

	bleEnableOnce sync.Once
	bleEnableErr  error

	bleMu      sync.Mutex
	bleCurrent *bleLink

	errNoAdapter = errors.New("kein Bluetooth-Adapter")
)

func mustUUID(s string) bluetooth.UUID {
	u, err := bluetooth.ParseUUID(s)
	if err != nil {
		panic(err)
	}
	return u
}

type bleLink struct {
	dev   bluetooth.Device
	rx    bluetooth.DeviceCharacteristic
	name  string
	addr  string
	id    string
	split lineSplitter
	smu   sync.Mutex
	done  chan struct{}
	once  sync.Once
	wmu   sync.Mutex
}

func (l *bleLink) Name() string          { return "Bluetooth (" + l.name + ")" }
func (l *bleLink) ID() string            { return l.id }
func (l *bleLink) Lines() <-chan string  { return l.split.out }
func (l *bleLink) Done() <-chan struct{} { return l.done }
func (l *bleLink) Send(line string) error {
	l.wmu.Lock()
	defer l.wmu.Unlock()
	_, err := l.rx.Write([]byte(line + "\n"))
	if err != nil {
		l.Close()
	}
	return err
}
func (l *bleLink) Close() {
	l.once.Do(func() {
		close(l.done)
		_ = l.dev.Disconnect()
		bleMu.Lock()
		if bleCurrent == l {
			bleCurrent = nil
		}
		bleMu.Unlock()
	})
}

func connectBLE(timeout time.Duration) (link, error) {
	adapter := bluetooth.DefaultAdapter
	bleEnableOnce.Do(func() {
		bleEnableErr = adapter.Enable()
		if bleEnableErr == nil {
			adapter.SetConnectHandler(func(d bluetooth.Device, connected bool) {
				if connected {
					return
				}
				bleMu.Lock()
				cur := bleCurrent
				bleMu.Unlock()
				if cur != nil && cur.addr == d.Address.String() {
					go cur.Close()
				}
			})
		}
	})
	if bleEnableErr != nil {
		return nil, errNoAdapter
	}

	var (
		mu    sync.Mutex
		found *bluetooth.ScanResult
	)
	timer := time.AfterFunc(timeout, func() { _ = adapter.StopScan() })
	err := adapter.Scan(func(a *bluetooth.Adapter, r bluetooth.ScanResult) {
		if !r.HasServiceUUID(nusService) {
			return
		}
		mu.Lock()
		if found == nil {
			rr := r
			found = &rr
		}
		mu.Unlock()
		_ = a.StopScan()
	})
	timer.Stop()
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, errors.New("kein Geraet in Reichweite")
	}

	dev, err := adapter.Connect(found.Address, bluetooth.ConnectionParams{})
	if err != nil {
		return nil, fmt.Errorf("verbinden: %w", err)
	}
	fail := func(e error) (link, error) {
		_ = dev.Disconnect()
		return nil, e
	}
	svcs, err := dev.DiscoverServices([]bluetooth.UUID{nusService})
	if err != nil || len(svcs) == 0 {
		return fail(fmt.Errorf("Dienst nicht gefunden: %v", err))
	}
	chars, err := svcs[0].DiscoverCharacteristics([]bluetooth.UUID{nusRX, nusTX})
	if err != nil {
		return fail(err)
	}
	l := &bleLink{dev: dev, addr: found.Address.String(), done: make(chan struct{})}
	l.split.out = make(chan string, 32)
	l.name = found.LocalName()
	if l.name == "" {
		l.name = l.addr
	}
	var haveRX, haveTX bool
	for _, c := range chars {
		switch c.UUID() {
		case nusRX:
			l.rx = c
			haveRX = true
		case nusTX:
			haveTX = true
			if err := c.EnableNotifications(func(buf []byte) {
				l.smu.Lock()
				l.split.feed(buf)
				l.smu.Unlock()
			}); err != nil {
				return fail(err)
			}
		}
	}
	if !haveRX || !haveTX {
		return fail(errors.New("Merkmale fehlen"))
	}
	bleMu.Lock()
	bleCurrent = l
	bleMu.Unlock()

	if id, ok := handshake(l, 3*time.Second); ok {
		l.id = id
		logf("Geraet per Bluetooth verbunden: %s (%s)", l.name, id)
		return l, nil
	}
	l.Close()
	return nil, errors.New("Geraet antwortet nicht")
}
