//go:build darwin

package main

// Mac-Version: laeuft als Hintergrunddienst (LaunchAgent). TokenOrb.app oeffnet das
// Einstellungsfenster (webui.go); im Terminal geht zusaetzlich:
//
//   ./tokenorb install     einrichten (Claude Code verbinden + Autostart)
//   tokenorb status        Verbindung und aktuelle Werte anzeigen
//   tokenorb demo          Demo-Modus am Geraet an/aus
//   tokenorb hell 60       Helligkeit 1..100
//   tokenorb ui            Einstellungsfenster oeffnen
//   tokenorb uninstall     alles wieder entfernen

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const agentLabel = "de.tokenorb.agent"

// Installiert wird entweder die ganze App (TokenOrb.app nach Application Support kopiert)
// oder – bei Installation aus dem Terminal – nur das Programm.
func installedAppPath() string { return filepath.Join(appDir(), "TokenOrb.app") }
func installedExePath() string {
	exe := filepath.Join(installedAppPath(), "Contents", "MacOS", "TokenOrb")
	if _, err := os.Stat(exe); err == nil {
		return exe
	}
	return filepath.Join(appDir(), "tokenorb")
}

// bundleOf liefert ".../TokenOrb.app", wenn self in einer App liegt.
func bundleOf(self string) string {
	if i := strings.Index(self, ".app/Contents/MacOS/"); i >= 0 {
		return self[:i+4]
	}
	return ""
}

func hookedExactly() bool {
	obj, err := readSettings(claudeSettingsPath())
	if err != nil {
		return false
	}
	sl, ok := currentStatusLine(obj)
	return ok && sl.Command == hookCommand(installedExePath())
}
func agentPlistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", agentLabel+".plist")
}
func cmdQueuePath() string   { return filepath.Join(appDir(), "befehle.txt") }
func statusFilePath() string { return filepath.Join(appDir(), "status.txt") }

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
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	out.Close()
	return os.Rename(tmp, dst)
}

func uid() string { return strconv.Itoa(os.Getuid()) }

func launchctl(args ...string) error {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s: %v %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

const plistTpl = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array><string>%s</string><string>run</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Interactive</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`

func xmlEsc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func install(self string) error {
	if b := bundleOf(self); b != "" && b != installedAppPath() {
		// ganze App kopieren (ditto behaelt die Signatur), alte Version vorher stoppen
		_ = launchctl("bootout", "gui/"+uid()+"/"+agentLabel)
		_ = os.MkdirAll(appDir(), 0o755)
		_ = os.RemoveAll(installedAppPath())
		if out, err := exec.Command("ditto", b, installedAppPath()).CombinedOutput(); err != nil {
			return fmt.Errorf("App kopieren: %v %s", err, out)
		}
		_ = exec.Command("xattr", "-dr", "com.apple.quarantine", installedAppPath()).Run()
		_ = os.Remove(filepath.Join(appDir(), "tokenorb")) // alte Einzeldatei aus 1.0/1.1
	}
	target := installedExePath()
	if bundleOf(self) == "" && self != target && !sameContent(self, target) {
		_ = launchctl("bootout", "gui/"+uid()+"/"+agentLabel) // alte Version stoppen
		if err := copyFile(self, target); err != nil {
			return fmt.Errorf("kopieren nach %s: %w", target, err)
		}
	}
	_ = exec.Command("xattr", "-d", "com.apple.quarantine", target).Run()

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

	plist := fmt.Sprintf(plistTpl, agentLabel, xmlEsc(target), xmlEsc(filepath.Join(appDir(), "fehler.log")))
	if err := os.MkdirAll(filepath.Dir(agentPlistPath()), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(agentPlistPath(), []byte(plist), 0o644); err != nil {
		return err
	}
	_ = launchctl("bootout", "gui/"+uid()+"/"+agentLabel)
	if err := launchctl("bootstrap", "gui/"+uid(), agentPlistPath()); err != nil {
		return err
	}
	logf("installiert nach %s (vorherige Statuszeile: %q)", target, prev)
	return nil
}

func uninstall() error {
	_ = launchctl("bootout", "gui/"+uid()+"/"+agentLabel)
	_ = os.Remove(agentPlistPath())
	if err := RemoveHook(claudeSettingsPath(), getConfig().ChainStatusLine); err != nil {
		return fmt.Errorf("Claude Code trennen: %w", err)
	}
	return nil
}

func queueCommand(line string) error {
	f, err := os.OpenFile(cmdQueuePath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}

func notify(msg string) {
	_ = exec.Command("osascript", "-e", fmt.Sprintf("display notification %q with title %q", msg, appName)).Run()
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

// runAgent ist der Hintergrunddienst.
func runAgent() {
	_ = os.MkdirAll(appDir(), 0o755)
	lock, err := os.OpenFile(filepath.Join(appDir(), "agent.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err == nil && syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		fmt.Fprintln(os.Stderr, "TokenOrb laeuft bereits.")
		os.Exit(0)
	}
	logf("%s %s gestartet (Mac)", appName, appVersion)
	if err := EnsureEventHooks(claudeSettingsPath()); err != nil {
		logf("Aktivitaets-Hooks: %v", err)
	}
	var dm *deviceManager
	dm = newDeviceManager(handleDeviceKey, func() {
		_ = os.WriteFile(statusFilePath(), []byte(dm.Status()+"\n"), 0o644)
	})
	_ = os.WriteFile(statusFilePath(), []byte(dm.Status()+"\n"), 0o644)
	// Befehle aus dem Terminal (tokenorb demo / hell) an das Geraet weiterreichen
	go func() {
		for range time.Tick(500 * time.Millisecond) {
			data, err := os.ReadFile(cmdQueuePath())
			if err != nil || len(data) == 0 {
				continue
			}
			_ = os.Remove(cmdQueuePath())
			reloadConfig()
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				if line = strings.TrimSpace(line); line != "" {
					dm.Command(line)
				}
			}
		}
	}()
	go runAccountPoller()
	go runUpdateChecker(upd, func(v string) { notify("Update " + v + " verfügbar – TokenOrb öffnen zum Installieren") })
	startWebUI(dm)
	dm.run()
}

func printStatus() {
	st, _ := os.ReadFile(statusFilePath())
	running := exec.Command("launchctl", "print", "gui/"+uid()+"/"+agentLabel).Run() == nil
	fmt.Println(appName, appVersion)
	if running {
		fmt.Println("Dienst:  laeuft")
	} else {
		fmt.Println("Dienst:  laeuft nicht (./tokenorb install)")
	}
	if len(st) > 0 {
		fmt.Print(strings.Replace(string(st), "Geraet:", "Geraet: ", 1))
	}
	if IsHooked(claudeSettingsPath()) {
		fmt.Println("Claude Code: verbunden")
	} else {
		fmt.Println("Claude Code: nicht verbunden")
	}
	if loadConfig().AccountMode {
		k, _ := os.ReadFile(filepath.Join(appDir(), "konto.txt"))
		fmt.Println("Konto-Modus:", strings.TrimSpace(string(k)))
	} else {
		fmt.Println("Konto-Modus: aus (tokenorb konto an)")
	}
	if u, err := readState(statePath()); err == nil {
		fmt.Println(SummaryText(u, time.Now()))
	} else {
		fmt.Println("Noch keine Werte – einmal in Claude Code etwas machen.")
	}
}

const usage = `TokenOrb fuer macOS

  ./tokenorb install     einrichten: Claude Code verbinden + startet automatisch
  tokenorb status        Verbindung und aktuelle Werte anzeigen
  tokenorb demo          Demo-Modus am Geraet an/aus
  tokenorb hell 60       Helligkeit 1..100
  tokenorb konto an      Limits direkt vom Konto holen (auch Claude-App/Browser)
  tokenorb ui            Einstellungsfenster oeffnen
  tokenorb uninstall     alles wieder entfernen

Nach "install" liegt das Programm unter
  ~/Library/Application Support/TokenOrb/tokenorb
`

func sameContent(a, b string) bool {
	x, err1 := os.ReadFile(a)
	y, err2 := os.ReadFile(b)
	return err1 == nil && err2 == nil && sha256.Sum256(x) == sha256.Sum256(y)
}

func agentRunning() bool {
	return exec.Command("launchctl", "print", "gui/"+uid()+"/"+agentLabel).Run() == nil
}

func dialog(msg string) {
	_ = exec.Command("osascript", "-e", fmt.Sprintf("display dialog %q with title %q buttons {\"OK\"} default button 1 with icon caution", msg, appName)).Run()
}

// Doppelklick auf TokenOrb.app: einrichten (falls noetig) und Einstellungen oeffnen.
func appLaunch(self string) {
	needInstall := !sameContent(self, installedExePath()) || !agentRunning() || !hookedExactly()
	if needInstall {
		if err := install(self); err != nil {
			dialog("TokenOrb konnte nicht eingerichtet werden:\n" + err.Error())
			return
		}
	}
	for i := 0; i < 40 && !uiUp(); i++ {
		time.Sleep(150 * time.Millisecond)
	}
	if !uiUp() {
		_ = launchctl("kickstart", "-k", "gui/"+uid()+"/"+agentLabel)
		for i := 0; i < 40 && !uiUp(); i++ {
			time.Sleep(150 * time.Millisecond)
		}
	}
	openAppWindow(uiURL)
}

func main() {
	self, _ := os.Executable()
	self, _ = filepath.EvalSymlinks(self)
	if len(os.Args) < 2 || strings.HasPrefix(os.Args[1], "-psn") {
		if strings.Contains(self, ".app/Contents/MacOS/") {
			appLaunch(self)
			return
		}
		fmt.Print(usage)
		return
	}
	switch strings.ToLower(os.Args[1]) {
	case "statusline":
		runStatusLine(os.Stdin, os.Stdout)
	case "hook":
		runHook(os.Stdin)
	case "run":
		runAgent()
	case "install":
		if err := install(self); err != nil {
			fmt.Println("Installation fehlgeschlagen:", err)
			os.Exit(1)
		}
		fmt.Println("Fertig! TokenOrb laeuft jetzt im Hintergrund und startet automatisch.")
		fmt.Println("Starte Claude Code einmal neu, damit die Statuszeile die Werte liefert.")
		fmt.Println("Pruefen mit:  \"" + installedExePath() + "\" status")
		notify("Installiert – Puck per USB anstecken")
	case "uninstall":
		if err := uninstall(); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		fmt.Println("Entfernt. Der Ordner", appDir(), "kann geloescht werden.")
	case "status":
		printStatus()
	case "ui":
		appLaunch(installedExePath())
	case "demo":
		on := "1"
		if len(os.Args) > 2 && (os.Args[2] == "aus" || os.Args[2] == "off" || os.Args[2] == "0") {
			on = "0"
		}
		if err := queueCommand("D " + on); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		fmt.Println("Demo", map[string]string{"1": "an", "0": "aus"}[on], "(\"tokenorb demo aus\" zum Beenden)")
	case "hell", "brightness":
		if len(os.Args) < 3 {
			fmt.Println("z.B.: tokenorb hell 60")
			os.Exit(1)
		}
		b, err := strconv.Atoi(os.Args[2])
		if err != nil || b < 1 || b > 100 {
			fmt.Println("Wert 1..100")
			os.Exit(1)
		}
		updateConfig(func(c *Config) { c.Brightness = b })
		_ = queueCommand(fmt.Sprintf("B %d", b))
		fmt.Println("Helligkeit", b)
	case "konto", "account":
		on := len(os.Args) < 3 || !(os.Args[2] == "aus" || os.Args[2] == "off" || os.Args[2] == "0")
		updateConfig(func(c *Config) { c.AccountMode = on })
		_ = queueCommand("P") // Dienst liest die Einstellung neu
		if on {
			fmt.Println("Konto-Modus an. TokenOrb holt die Limits jetzt alle 3 Minuten direkt vom Konto –")
			fmt.Println("damit zaehlt auch Arbeit in der Claude-App und im Browser.")
			fmt.Println("Hinweis: nutzt eine nicht offiziell dokumentierte Anthropic-Adresse und die")
			fmt.Println("Anmeldung von Claude Code. Abschalten: tokenorb konto aus")
		} else {
			fmt.Println("Konto-Modus aus. Werte kommen nur noch ueber die Claude-Code-Statuszeile.")
		}
	case "--version", "version":
		fmt.Println(appName, appVersion)
	default:
		fmt.Print(usage)
	}
}
