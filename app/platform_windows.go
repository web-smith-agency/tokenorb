//go:build windows

package main

// Windows-Teile fuer Einstellungsfenster, Updates und Fenster.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/systray"
)

var upd = newUpdater("windows")

var keyActions = [][2]string{
	{"shift+tab", "Shift+Tab – Modus wechseln"},
	{"esc", "Esc – abbrechen"},
	{"enter", "Enter"},
	{"ctrl+c", "Strg+C"},
	{"none", "nichts"},
}

func platformName() string { return "windows" }

func validateCombo(s string) error {
	for _, part := range strings.Split(s, "+") {
		if _, ok := vkFor(part); !ok {
			return errors.New("unbekannte Taste: " + part)
		}
	}
	return nil
}

func autostartEnabled() bool { return isAutostart() }

func setAutostartEnabled(on bool) error { return setAutostart(on, installedExePath()) }

func platformSetting(key string, v any) error {
	switch key {
	case "testkey":
		go func() {
			time.Sleep(2 * time.Second)
			if err := sendCombo(getConfig().Button1); err != nil {
				logf("Test-Taste: %v", err)
			}
		}()
		return nil
	case "folder":
		openFolder(appDir())
		return nil
	}
	return errors.New("unbekannt: " + key)
}

// openAppWindow oeffnet das Einstellungsfenster als eigenes Fenster (Edge/Chrome im
// App-Modus, beide sind praktisch immer installiert), sonst im Standardbrowser.
func openAppWindow(url string) {
	var cands []string
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LOCALAPPDATA"} {
		base := os.Getenv(env)
		if base == "" {
			continue
		}
		cands = append(cands,
			filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"))
	}
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			if exec.Command(c, "--app="+url, "--window-size=540,900").Start() == nil {
				return
			}
		}
	}
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

// applyAppUpdate startet den neuen Installer still; der beendet TokenOrb, ersetzt die
// Dateien und startet TokenOrb wieder.
func applyAppUpdate(name string, data []byte) error {
	dir := filepath.Join(os.TempDir(), "TokenOrb-Update")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o755); err != nil {
		return err
	}
	logf("Update: starte %s /S", path)
	if err := exec.Command(path, "/S").Start(); err != nil {
		return err
	}
	go func() {
		time.Sleep(800 * time.Millisecond)
		systray.Quit()
	}()
	return nil
}
