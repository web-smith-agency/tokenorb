//go:build darwin

package main

// Mac-Teile fuer Einstellungsfenster, Updates und Fenster.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

var upd = newUpdater("darwin")

var keyActions = [][2]string{
	{"shift+tab", "Shift+Tab – Modus wechseln"},
	{"esc", "Esc – abbrechen"},
	{"enter", "Enter"},
	{"cmd+k", "Cmd+K"},
	{"none", "nichts"},
}

func platformName() string { return "mac" }

func validateCombo(s string) error {
	_, err := appleScriptFor(s)
	return err
}

// Autostart ist auf dem Mac der LaunchAgent – immer an, solange installiert.
func autostartEnabled() bool            { return agentRunning() }
func setAutostartEnabled(on bool) error { return errors.New("auf dem Mac startet TokenOrb immer mit") }

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
	case "accessibility":
		_ = exec.Command("open", "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility").Start()
		return nil
	case "folder":
		_ = exec.Command("open", appDir()).Start()
		return nil
	}
	return errors.New("unbekannt: " + key)
}

// openAppWindow: Chrome/Edge im App-Modus (eigenes Fenster), sonst Standardbrowser.
func openAppWindow(url string) {
	for _, app := range []string{"Google Chrome", "Microsoft Edge", "Brave Browser"} {
		if _, err := os.Stat(filepath.Join("/Applications", app+".app")); err == nil {
			if exec.Command("open", "-na", app, "--args", "--app="+url, "--window-size=540,900").Run() == nil {
				return
			}
		}
	}
	_ = exec.Command("open", url).Run()
}

// applyAppUpdate entpackt die neue App und laesst sie sich selbst installieren. Das
// ersetzt den laufenden Dienst – deshalb in einer eigenen Sitzung starten.
func applyAppUpdate(name string, data []byte) error {
	dir, err := os.MkdirTemp("", "TokenOrb-Update")
	if err != nil {
		return err
	}
	zip := filepath.Join(dir, name)
	if err := os.WriteFile(zip, data, 0o644); err != nil {
		return err
	}
	out := filepath.Join(dir, "neu")
	if b, err := exec.Command("ditto", "-x", "-k", zip, out).CombinedOutput(); err != nil {
		return errors.New("entpacken: " + string(b))
	}
	matches, _ := filepath.Glob(filepath.Join(out, "*", "TokenOrb.app"))
	if m2, _ := filepath.Glob(filepath.Join(out, "TokenOrb.app")); len(m2) > 0 {
		matches = m2
	}
	if len(matches) == 0 {
		return errors.New("TokenOrb.app fehlt im Update")
	}
	exe := filepath.Join(matches[0], "Contents", "MacOS", "TokenOrb")
	_ = exec.Command("xattr", "-dr", "com.apple.quarantine", matches[0]).Run()
	cmd := exec.Command(exe, "install", "--quiet")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	logf("Update: %s install", exe)
	return cmd.Start()
}
