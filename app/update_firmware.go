//go:build windows || darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// StartFirmwareUpdate laedt die Firmware fuer das verbundene Board und spielt sie auf.
func (u *updater) StartFirmwareUpdate(dm *deviceManager) error {
	id, _ := dm.Device()
	_, board := parseDeviceID(id)
	name, ok := firmwareAssets[board]
	if id == "" {
		return errors.New("kein Puck verbunden")
	}
	if !ok {
		return fmt.Errorf("unbekanntes Board %q", board)
	}
	if !u.setBusy("firmware") {
		return errors.New("es läuft bereits ein Update")
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		data, err := u.Download(ctx, name, func(p int) { u.setProgress(p / 5) }) // Download = 0..20 %
		if err != nil {
			u.finish(err.Error(), true)
			return
		}
		u.note("Übertrage auf den Puck … nicht abstecken")
		stop := make(chan struct{})
		go func() {
			t := time.NewTicker(200 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case <-t.C:
					if p := dm.FlashProgress(); p >= 0 {
						u.setProgress(20 + p*80/100)
					}
				}
			}
		}()
		err = dm.Flash(data)
		close(stop)
		if err != nil {
			u.finish(err.Error(), true)
			return
		}
		u.finish("Firmware aktualisiert – der Puck startet neu.", false)
	}()
	return nil
}
