//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func installedExePath() string { return filepath.Join(appDir(), appName+".exe") }

func samePath(a, b string) bool {
	ea, _ := filepath.EvalSymlinks(a)
	eb, _ := filepath.EvalSymlinks(b)
	if ea == "" {
		ea = a
	}
	if eb == "" {
		eb = b
	}
	return strings.EqualFold(filepath.Clean(ea), filepath.Clean(eb))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".new"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	out.Close()
	_ = os.Remove(dst)
	return os.Rename(tmp, dst)
}

// install kopiert die App, verbindet Claude Code und richtet den Autostart ein.
func install(self string) error {
	target := installedExePath()
	if err := copyFile(self, target); err != nil {
		return fmt.Errorf("kopieren nach %s: %w", target, err)
	}
	cfg := loadConfig()
	prev, err := InstallHook(claudeSettingsPath(), hookCommand(target))
	if err != nil {
		return fmt.Errorf("Claude Code verbinden: %w", err)
	}
	if prev != "" {
		cfg.ChainStatusLine = prev
	}
	if err := saveConfig(cfg); err != nil {
		return err
	}
	if err := setAutostart(true, target); err != nil {
		logf("Autostart: %v", err)
	}
	logf("installiert nach %s (vorherige Statuszeile: %q)", target, prev)
	return nil
}

// setupAfterInstall wird vom Installer aufgerufen (TokenOrb.exe --setup): Claude Code
// verbinden und beim ersten Mal den Autostart einschalten. Bei Updates bleibt die Wahl.
func setupAfterInstall() error {
	target := installedExePath()
	cfg := loadConfig()
	prev, err := InstallHook(claudeSettingsPath(), hookCommand(target))
	if err != nil {
		return fmt.Errorf("Claude Code verbinden: %w", err)
	}
	if prev != "" {
		cfg.ChainStatusLine = prev
	}
	first := !cfg.SetupDone
	cfg.SetupDone = true
	if err := saveConfig(cfg); err != nil {
		return err
	}
	if first || isAutostart() {
		if err := setAutostart(true, target); err != nil {
			logf("Autostart: %v", err)
		}
	}
	logf("eingerichtet (%s, erstes Mal: %v)", appVersion, first)
	return nil
}

// removeForUninstall wird von der Deinstallation aufgerufen (TokenOrb.exe --remove).
func removeForUninstall() {
	_ = RemoveHook(claudeSettingsPath(), loadConfig().ChainStatusLine)
	_ = setAutostart(false, installedExePath())
	logf("fuer Deinstallation entfernt")
}

func main() {
	openWindow := false
	if len(os.Args) > 1 {
		switch strings.ToLower(os.Args[1]) {
		case "statusline":
			runStatusLine(os.Stdin, os.Stdout)
			return
		case "--version", "version":
			fmt.Println(appName, appVersion)
			return
		case "--setup":
			if err := setupAfterInstall(); err != nil {
				logf("Einrichtung: %v", err)
				os.Exit(1)
			}
			return
		case "--remove":
			removeForUninstall()
			return
		case "--open":
			openWindow = true
		}
	}

	self, _ := os.Executable()
	portable := len(os.Args) > 1 && os.Args[1] == "--portable"

	if !portable && !samePath(self, installedExePath()) {
		if isAlreadyRunning() {
			openAppWindow(uiURL)
			return
		}
		msg := "TokenOrb installieren?\n\n" +
			"- App wird nach " + appDir() + " kopiert\n" +
			"- Claude Code Statuszeile wird verbunden (vorhandene bleibt erhalten)\n" +
			"- startet automatisch mit Windows\n\n" +
			"Keine Adminrechte noetig. Tipp: Der Installer TokenOrb-Setup.exe macht das gleiche und legt zusaetzlich einen Startmenue-Eintrag an."
		if messageBox(msg, mbYesNo|mbIconQuest) != idYes {
			return
		}
		if err := install(self); err != nil {
			messageBox("Installation fehlgeschlagen:\n"+err.Error(), mbIconWarning)
			return
		}
		_ = exec.Command(installedExePath(), "--open").Start()
		return
	}

	if !acquireSingleInstance() {
		// laeuft schon (z.B. Startmenue-Eintrag erneut angeklickt) -> Fenster zeigen
		openAppWindow(uiURL)
		return
	}
	if !loadConfig().SetupDone {
		// erster Start ohne Installer (z.B. nach "install" oben)
		openWindow = true
		updateConfig(func(c *Config) { c.SetupDone = true })
	}
	runTray(openWindow)
}
