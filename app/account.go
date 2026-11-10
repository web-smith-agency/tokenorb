package main

// Konto-Modus: holt die Limits direkt vom Anthropic-Konto, damit auch Arbeit in der
// Claude-App oder im Browser sofort sichtbar ist (die Statuszeile laeuft nur in Claude Code).
//
// ACHTUNG: api.anthropic.com/api/oauth/usage ist NICHT offiziell dokumentiert. Die Adresse
// kann sich jederzeit aendern. Deshalb ist der Modus aus, bis man ihn bewusst einschaltet,
// und die Statuszeile funktioniert unabhaengig davon weiter.
//
// Anmeldung: der OAuth-Token, den Claude Code selbst speichert (macOS: Schluesselbund
// "Claude Code-credentials", sonst ~/.claude/.credentials.json). TokenOrb liest ihn nur,
// schreibt nie Zugangsdaten. Ist er abgelaufen, wird hoechstens alle 30 Minuten Claude Code
// mit einer Mini-Anfrage gestartet, damit es den Token selbst erneuert.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	accountURL      = "https://api.anthropic.com/api/oauth/usage"
	accountInterval = 3 * time.Minute
	refreshCooldown = 30 * time.Minute
)

type oauthCreds struct {
	ClaudeAiOauth struct {
		AccessToken string  `json:"accessToken"`
		ExpiresAt   float64 `json:"expiresAt"` // Millisekunden
	} `json:"claudeAiOauth"`
}

var errNoToken = errors.New("keine Claude-Code-Anmeldung gefunden")
var errExpired = errors.New("Anmeldung abgelaufen")

func readCredentialBlob() ([]byte, error) {
	if runtime.GOOS == "darwin" {
		user := os.Getenv("USER")
		out, err := exec.Command("security", "find-generic-password", "-s", "Claude Code-credentials", "-a", user, "-w").Output()
		if err == nil && len(out) > 0 {
			return out, nil
		}
	}
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".claude")
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		dir = d
	}
	return os.ReadFile(filepath.Join(dir, ".credentials.json"))
}

func accountToken(now time.Time) (string, error) {
	blob, err := readCredentialBlob()
	if err != nil {
		return "", errNoToken
	}
	var c oauthCreds
	if json.Unmarshal([]byte(strings.TrimSpace(string(blob))), &c) != nil || c.ClaudeAiOauth.AccessToken == "" {
		return "", errNoToken
	}
	if c.ClaudeAiOauth.ExpiresAt > 0 && now.Add(2*time.Minute).UnixMilli() >= int64(c.ClaudeAiOauth.ExpiresAt) {
		return c.ClaudeAiOauth.AccessToken, errExpired
	}
	return c.ClaudeAiOauth.AccessToken, nil
}

type accountWindow struct {
	Utilization *float64        `json:"utilization"`
	ResetsAt    json.RawMessage `json:"resets_at"`
}

// ParseAccountUsage wandelt die Antwort des Kontos in unseren Zustand um.
func ParseAccountUsage(data []byte, now time.Time) (UsageState, bool) {
	var p struct {
		FiveHour *accountWindow `json:"five_hour"`
		SevenDay *accountWindow `json:"seven_day"`
	}
	if json.Unmarshal(data, &p) != nil {
		return UsageState{}, false
	}
	conv := func(w *accountWindow) *Window {
		if w == nil || w.Utilization == nil {
			return nil
		}
		return &Window{UsedPercentage: *w.Utilization, ResetsAt: parseResetsAt(w.ResetsAt)}
	}
	st := UsageState{UpdatedAt: now.Unix(), FiveHour: conv(p.FiveHour), SevenDay: conv(p.SevenDay)}
	if st.FiveHour == nil && st.SevenDay == nil {
		return st, false
	}
	return st, true
}

var httpClient = &http.Client{Timeout: 20 * time.Second}

func fetchAccount(token string) ([]byte, int, error) {
	req, _ := http.NewRequest("GET", accountURL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("User-Agent", "tokenorb/"+appVersion)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for len(buf) < 1<<16 {
		n, rerr := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if rerr != nil {
			break
		}
	}
	return buf, resp.StatusCode, nil
}

func claudeBinary() string {
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{
		filepath.Join(home, ".local", "bin", "claude"), filepath.Join(home, ".claude", "local", "claude"),
		"/opt/homebrew/bin/claude", "/usr/local/bin/claude",
		filepath.Join(home, ".local", "bin", "claude.exe"), filepath.Join(home, "AppData", "Roaming", "npm", "claude.cmd"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// nudgeClaude laesst Claude Code seinen Token selbst erneuern (kostet eine winzige Haiku-Anfrage).
func nudgeClaude() error {
	bin := claudeBinary()
	if bin == "" {
		return errors.New("claude-Befehl nicht gefunden")
	}
	home, _ := os.UserHomeDir()
	cmd := exec.Command(bin, "-p", "hi", "--model", "haiku", "--max-turns", "1",
		"--output-format", "json", "--setting-sources", "", "--settings", `{"verbose":false}`)
	cmd.Dir = home
	hideWindow(cmd)
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(90 * time.Second):
		_ = cmd.Process.Kill()
		return errors.New("Zeitueberschreitung")
	}
}

type accountPoller struct {
	mu        sync.Mutex
	status    string
	lastNudge time.Time
}

var account = &accountPoller{status: "aus"}

func (a *accountPoller) Status() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

func (a *accountPoller) set(s string) {
	a.mu.Lock()
	a.status = s
	a.mu.Unlock()
	_ = os.WriteFile(filepath.Join(appDir(), "konto.txt"), []byte(s+"\n"), 0o644)
}

func (a *accountPoller) once() {
	now := time.Now()
	token, err := accountToken(now)
	if errors.Is(err, errExpired) && time.Since(a.lastNudge) > refreshCooldown {
		a.lastNudge = time.Now()
		a.set("Anmeldung wird erneuert ...")
		if nerr := nudgeClaude(); nerr != nil {
			logf("Konto: Erneuern fehlgeschlagen: %v", nerr)
		}
		token, err = accountToken(time.Now())
	}
	if err != nil {
		if errors.Is(err, errExpired) {
			a.set("Anmeldung abgelaufen – Claude Code einmal im Terminal starten")
		} else {
			a.set("keine Claude-Code-Anmeldung gefunden – einmal 'claude' im Terminal starten")
		}
		return
	}
	data, code, ferr := fetchAccount(token)
	switch {
	case ferr != nil:
		a.set("Netzwerkfehler")
		return
	case code == 401 || code == 403:
		a.set(fmt.Sprintf("Konto lehnt ab (HTTP %d)", code))
		a.lastNudge = time.Time{} // beim naechsten Mal erneuern versuchen
		return
	case code == 429:
		a.set("zu viele Anfragen, wartet")
		return
	case code != 200:
		a.set(fmt.Sprintf("keine Daten (HTTP %d)", code))
		return
	}
	st, ok := ParseAccountUsage(data, time.Now())
	if !ok {
		a.set("Antwort nicht verstanden")
		return
	}
	if prev, err := readState(statePath()); err == nil && prev.Model != "" {
		st.Model = prev.Model
	}
	if err := writeState(statePath(), st); err != nil {
		a.set("Speichern fehlgeschlagen")
		return
	}
	a.set("an, zuletzt " + time.Now().Format("15:04"))
}

// runAccountPoller laeuft im Hintergrund und fragt alle paar Minuten ab, solange der Modus an ist.
func runAccountPoller() {
	var last time.Time
	wasOn := false
	for {
		on := loadConfig().AccountMode
		if on && (!wasOn || time.Since(last) >= accountInterval) {
			account.once()
			last = time.Now()
		} else if !on && wasOn {
			account.set("aus")
		}
		wasOn = on
		time.Sleep(10 * time.Second)
	}
}
