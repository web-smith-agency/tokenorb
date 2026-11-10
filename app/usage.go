package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Window ist ein Limit-Fenster (5 Stunden oder 7 Tage).
type Window struct {
	UsedPercentage float64 `json:"used_percentage"`
	ResetsAt       int64   `json:"resets_at"` // Unix-Sekunden
}

// UsageState wird von der Statuszeile geschrieben und von der Tray-App gelesen.
type UsageState struct {
	UpdatedAt int64   `json:"updated_at"` // Unix-Sekunden
	Model     string  `json:"model,omitempty"`
	FiveHour  *Window `json:"five_hour,omitempty"`
	SevenDay  *Window `json:"seven_day,omitempty"`
}

// --- Eingabe von Claude Code (JSON auf stdin der Statuszeile) -------------

type ccWindow struct {
	UsedPercentage *float64        `json:"used_percentage"`
	ResetsAt       json.RawMessage `json:"resets_at"`
}

type ccInput struct {
	Model struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	RateLimits *struct {
		FiveHour *ccWindow `json:"five_hour"`
		SevenDay *ccWindow `json:"seven_day"`
	} `json:"rate_limits"`
}

// parseResetsAt akzeptiert Unix-Sekunden, Unix-Millisekunden oder ISO-8601,
// weil Claude Code je nach Version unterschiedliche Formate geliefert hat.
func parseResetsAt(raw json.RawMessage) int64 {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return 0
	}
	if strings.HasPrefix(s, `"`) {
		var str string
		if json.Unmarshal(raw, &str) != nil {
			return 0
		}
		if t, err := time.Parse(time.RFC3339Nano, str); err == nil {
			return t.Unix()
		}
		s = str
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 0
	}
	if f > 1e12 { // Millisekunden
		f /= 1000
	}
	return int64(f)
}

func convertWindow(w *ccWindow) *Window {
	if w == nil || w.UsedPercentage == nil {
		return nil
	}
	p := *w.UsedPercentage
	if math.IsNaN(p) || math.IsInf(p, 0) {
		return nil
	}
	p = math.Max(0, math.Min(100, p))
	return &Window{UsedPercentage: p, ResetsAt: parseResetsAt(w.ResetsAt)}
}

// ParseStatusLineInput liest das JSON, das Claude Code an die Statuszeile gibt.
// ok == false, wenn keine Limit-Daten enthalten sind (z.B. API-Key statt Abo,
// oder vor der ersten Antwort einer Sitzung).
func ParseStatusLineInput(data []byte, now time.Time) (st UsageState, ok bool) {
	var in ccInput
	if err := json.Unmarshal(data, &in); err != nil {
		return st, false
	}
	st.UpdatedAt = now.Unix()
	st.Model = in.Model.DisplayName
	if in.RateLimits != nil {
		st.FiveHour = convertWindow(in.RateLimits.FiveHour)
		st.SevenDay = convertWindow(in.RateLimits.SevenDay)
	}
	return st, st.FiveHour != nil || st.SevenDay != nil
}

// --- Datei ------------------------------------------------------------------

func readState(path string) (UsageState, error) {
	var st UsageState
	data, err := os.ReadFile(path)
	if err != nil {
		return st, err
	}
	err = json.Unmarshal(data, &st)
	return st, err
}

// writeFileAtomic schreibt erst in eine temporaere Datei und benennt dann um,
// damit der Leser nie eine halbe Datei sieht.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func writeState(path string, st UsageState) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

// --- Protokoll zum Geraet ---------------------------------------------------

func fmtPct(w *Window) string {
	if w == nil {
		return "-"
	}
	return strconv.FormatFloat(w.UsedPercentage, 'f', 1, 64)
}

func fmtRemain(w *Window, now time.Time) string {
	if w == nil || w.ResetsAt == 0 {
		return "-"
	}
	r := w.ResetsAt - now.Unix()
	if r < 0 {
		r = 0
	}
	return strconv.FormatInt(r, 10)
}

// DeviceUpdateLine baut die Zeile "U <p5> <r5> <p7> <r7> <alter>" fuer das Geraet.
func DeviceUpdateLine(st UsageState, now time.Time) string {
	age := now.Unix() - st.UpdatedAt
	if age < 0 || st.UpdatedAt == 0 {
		age = 0
	}
	return fmt.Sprintf("U %s %s %s %s %d",
		fmtPct(st.FiveHour), fmtRemain(st.FiveHour, now),
		fmtPct(st.SevenDay), fmtRemain(st.SevenDay, now), age)
}

// --- Anzeige ----------------------------------------------------------------

func fmtDuration(sec int64) string {
	if sec < 0 {
		return "--"
	}
	switch {
	case sec >= 86400:
		return fmt.Sprintf("%dT %dh", sec/86400, (sec%86400)/3600)
	case sec >= 3600:
		return fmt.Sprintf("%dh %02dm", sec/3600, (sec%3600)/60)
	case sec >= 60:
		return fmt.Sprintf("%dm", sec/60)
	default:
		return "<1m"
	}
}

// windowNow liefert Prozent und Restzeit; ist der Reset schon vorbei, ist das
// Fenster wieder bei 0 %.
func windowNow(w *Window, now time.Time) (pct float64, remain int64, ok bool) {
	if w == nil {
		return 0, -1, false
	}
	if w.ResetsAt == 0 {
		return w.UsedPercentage, -1, true
	}
	r := w.ResetsAt - now.Unix()
	if r <= 0 {
		return 0, 0, true
	}
	return w.UsedPercentage, r, true
}

// SummaryText ist der kurze Text fuer Tray-Tooltip und eigene Statuszeile.
func SummaryText(st UsageState, now time.Time) string {
	var parts []string
	if p, r, ok := windowNow(st.FiveHour, now); ok {
		s := fmt.Sprintf("Session %.0f%%", p)
		if r > 0 {
			s += " (Reset " + fmtDuration(r) + ")"
		}
		parts = append(parts, s)
	}
	if p, _, ok := windowNow(st.SevenDay, now); ok {
		parts = append(parts, fmt.Sprintf("Woche %.0f%%", p))
	}
	if len(parts) == 0 {
		return "keine Limit-Daten"
	}
	return strings.Join(parts, " · ")
}
