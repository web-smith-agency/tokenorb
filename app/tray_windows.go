//go:build windows

package main

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/systray"
)

var buttonActions = []struct{ label, combo string }{
	{"Shift+Tab (Modus wechseln)", "shift+tab"},
	{"Esc (abbrechen)", "esc"},
	{"Enter", "enter"},
	{"Aus", "none"},
}

var brightnessLevels = []int{100, 60, 30, 10}

func runTray(openWindow bool) {
	systray.Run(func() { onTrayReady(openWindow) }, func() { logf("beendet") })
}

func onTrayReady(openWindow bool) {
	logf("TokenOrb %s gestartet", appVersion)
	if err := EnsureEventHooks(claudeSettingsPath()); err != nil {
		logf("Aktivitaets-Hooks: %v", err)
	}
	systray.SetIcon(trayIcon(0, false))
	systray.SetTitle(appName)
	systray.SetTooltip(appName + ": warte auf Daten")

	mOpen := systray.AddMenuItem("TokenOrb öffnen …", "Einstellungen, Updates, Status")
	mUpdate := systray.AddMenuItem("Update installieren", "")
	mUpdate.Hide()
	systray.AddSeparator()
	mUsage := systray.AddMenuItem("Warte auf Claude Code ...", "")
	mUsage.Disable()
	mDevice := systray.AddMenuItem("Geraet: suche ...", "")
	mDevice.Disable()
	systray.AddSeparator()

	mHook := systray.AddMenuItemCheckbox("Mit Claude Code verbinden", "Statuszeile von Claude Code mitlesen", IsHooked(claudeSettingsPath()))
	mAuto := systray.AddMenuItemCheckbox("Mit Windows starten", "", isAutostart())
	mBLE := systray.AddMenuItemCheckbox("Bluetooth verwenden", "Wenn kein USB-Kabel steckt", getConfig().UseBluetooth)

	mKey := systray.AddMenuItem("Taste 2 am Geraet", "")
	mKeyShort := mKey.AddSubMenuItem("Kurz druecken", "")
	mKeyLong := mKey.AddSubMenuItem("Lang druecken", "")
	var shortItems, longItems []*systray.MenuItem
	for _, a := range buttonActions {
		shortItems = append(shortItems, mKeyShort.AddSubMenuItemCheckbox(a.label, "", strings.EqualFold(getConfig().Button1, a.combo)))
		longItems = append(longItems, mKeyLong.AddSubMenuItemCheckbox(a.label, "", strings.EqualFold(getConfig().Button2, a.combo)))
	}

	mBright := systray.AddMenuItem("Helligkeit", "")
	var brightItems []*systray.MenuItem
	for _, b := range brightnessLevels {
		brightItems = append(brightItems, mBright.AddSubMenuItemCheckbox(fmt.Sprintf("%d %%", b), "", getConfig().Brightness == b))
	}
	mDemo := systray.AddMenuItemCheckbox("Demo-Modus am Geraet", "Animierte Beispielwerte", false)

	systray.AddSeparator()
	mFolder := systray.AddMenuItem("Ordner oeffnen", "")
	mUninstall := systray.AddMenuItem("Deinstallieren ...", "")
	mQuit := systray.AddMenuItem("Beenden", "")

	refreshDevice := make(chan struct{}, 1)
	dm := newDeviceManager(handleDeviceKey, func() {
		select {
		case refreshDevice <- struct{}{}:
		default:
		}
	})
	go dm.run()
	startWebUI(dm)
	go runAccountPoller()
	go runUpdateChecker(upd, func(v string) {
		mUpdate.SetTitle("Update auf " + v + " installieren")
		mUpdate.Show()
	})
	systray.SetOnTapped(func() { openAppWindow(uiURL) })
	if openWindow {
		go func() {
			for i := 0; i < 30 && !uiUp(); i++ {
				time.Sleep(100 * time.Millisecond)
			}
			openAppWindow(uiURL)
		}()
	}
	go func() {
		for range mOpen.ClickedCh {
			openAppWindow(uiURL)
		}
	}()
	go func() {
		for range mUpdate.ClickedCh {
			if err := upd.StartAppUpdate(); err != nil {
				messageBox("Update: "+err.Error(), mbIconWarning)
				continue
			}
			openAppWindow(uiURL) // Fortschritt anzeigen
		}
	}()

	// Anzeige aktualisieren
	go func() {
		lastIcon := ""
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		update := func() {
			mDevice.SetTitle(dm.Status())
			st, err := readState(statePath())
			now := time.Now()
			if err != nil {
				mUsage.SetTitle("Warte auf Claude Code ...")
				systray.SetTooltip(appName + ": warte auf Daten")
			} else {
				txt := SummaryText(st, now)
				age := now.Unix() - st.UpdatedAt
				if age > 15*60 {
					txt += fmt.Sprintf("  (Stand vor %s)", fmtDuration(age))
				}
				mUsage.SetTitle(txt)
				systray.SetTooltip(appName + "\n" + SummaryText(st, now))
			}
			pct, _, ok := windowNow(st.FiveHour, now)
			key := fmt.Sprintf("%v-%d", ok, int(math.Round(pct/5)))
			if key != lastIcon {
				systray.SetIcon(trayIcon(pct, ok))
				lastIcon = key
			}
		}
		update()
		for {
			select {
			case <-tick.C:
				update()
			case <-refreshDevice:
				update()
			}
		}
	}()

	exe, _ := os.Executable()

	go func() {
		for range mHook.ClickedCh {
			path := claudeSettingsPath()
			if mHook.Checked() {
				if err := RemoveHook(path, getConfig().ChainStatusLine); err != nil {
					messageBox("Konnte settings.json nicht aendern:\n"+err.Error(), mbIconWarning)
					continue
				}
				updateConfig(func(c *Config) { c.ChainStatusLine = "" })
				mHook.Uncheck()
			} else {
				prev, err := InstallHook(path, hookCommand(exe))
				if err != nil {
					messageBox("Konnte settings.json nicht aendern:\n"+err.Error(), mbIconWarning)
					continue
				}
				if prev != "" {
					updateConfig(func(c *Config) { c.ChainStatusLine = prev })
				}
				mHook.Check()
				messageBox("Verbunden. Starte Claude Code neu, damit die Statuszeile aktiv wird.", mbIconInfo)
			}
		}
	}()

	go func() {
		for range mAuto.ClickedCh {
			on := !mAuto.Checked()
			if err := setAutostart(on, exe); err != nil {
				messageBox("Autostart konnte nicht geaendert werden:\n"+err.Error(), mbIconWarning)
				continue
			}
			if on {
				mAuto.Check()
			} else {
				mAuto.Uncheck()
			}
		}
	}()

	go func() {
		for range mBLE.ClickedCh {
			on := !mBLE.Checked()
			updateConfig(func(c *Config) { c.UseBluetooth = on })
			if on {
				mBLE.Check()
			} else {
				mBLE.Uncheck()
			}
		}
	}()

	radio := func(items []*systray.MenuItem, onPick func(i int)) {
		for i, it := range items {
			go func(i int, it *systray.MenuItem) {
				for range it.ClickedCh {
					for j, other := range items {
						if j == i {
							other.Check()
						} else {
							other.Uncheck()
						}
					}
					onPick(i)
				}
			}(i, it)
		}
	}
	radio(shortItems, func(i int) { updateConfig(func(c *Config) { c.Button1 = buttonActions[i].combo }) })
	radio(longItems, func(i int) { updateConfig(func(c *Config) { c.Button2 = buttonActions[i].combo }) })
	radio(brightItems, func(i int) {
		b := brightnessLevels[i]
		updateConfig(func(c *Config) { c.Brightness = b })
		dm.Command(fmt.Sprintf("B %d", b))
	})

	go func() {
		for range mDemo.ClickedCh {
			if mDemo.Checked() {
				mDemo.Uncheck()
				dm.Command("D 0")
			} else {
				mDemo.Check()
				dm.Command("D 1")
			}
		}
	}()

	go func() {
		for range mFolder.ClickedCh {
			openFolder(appDir())
		}
	}()

	go func() {
		for range mUninstall.ClickedCh {
			if un := filepath.Join(appDir(), "Uninstall.exe"); fileExists(un) {
				// mit dem Installer eingerichtet -> dessen Deinstallation nutzen
				if exec.Command(un).Start() == nil {
					systray.Quit()
					return
				}
			}
			if messageBox("TokenOrb wirklich entfernen?\n\nDie Claude-Code-Statuszeile wird zurueckgesetzt und der Autostart entfernt.", mbYesNo|mbIconQuest) != idYes {
				continue
			}
			_ = RemoveHook(claudeSettingsPath(), getConfig().ChainStatusLine)
			_ = setAutostart(false, exe)
			messageBox("Erledigt. Der Programmordner wird geoeffnet – du kannst ihn loeschen, sobald TokenOrb beendet ist.", mbIconInfo)
			openFolder(appDir())
			systray.Quit()
			return
		}
	}()

	go func() {
		<-mQuit.ClickedCh
		systray.Quit()
	}()
}

func handleDeviceKey(n int) {
	cfg := getConfig()
	combo := cfg.Button1
	if n == 2 {
		combo = cfg.Button2
	}
	if err := sendCombo(combo); err != nil {
		logf("Taste %d: %v", n, err)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
