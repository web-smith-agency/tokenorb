package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseStatusLineFormats(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cases := []struct {
		name  string
		json  string
		reset int64
	}{
		{"epoch-sekunden", `{"rate_limits":{"five_hour":{"used_percentage":23.5,"resets_at":1800003600},"seven_day":{"used_percentage":41,"resets_at":1800300000}}}`, 1800003600},
		{"epoch-ms", `{"rate_limits":{"five_hour":{"used_percentage":23.5,"resets_at":1800003600000}}}`, 1800003600},
		{"iso", `{"rate_limits":{"five_hour":{"used_percentage":23.5,"resets_at":"2027-01-15T08:00:00Z"}}}`, time.Date(2027, 1, 15, 8, 0, 0, 0, time.UTC).Unix()},
	}
	for _, c := range cases {
		st, ok := ParseStatusLineInput([]byte(c.json), now)
		if !ok || st.FiveHour == nil {
			t.Fatalf("%s: keine Daten", c.name)
		}
		if st.FiveHour.UsedPercentage != 23.5 || st.FiveHour.ResetsAt != c.reset {
			t.Errorf("%s: got %+v", c.name, *st.FiveHour)
		}
	}
	// ohne rate_limits (API-Nutzer oder Sitzungsstart)
	if _, ok := ParseStatusLineInput([]byte(`{"model":{"display_name":"Opus"}}`), now); ok {
		t.Error("ohne rate_limits darf ok nicht true sein")
	}
	if _, ok := ParseStatusLineInput([]byte(`kaputt`), now); ok {
		t.Error("kaputtes JSON")
	}
}

func TestDeviceUpdateLine(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	st := UsageState{UpdatedAt: now.Unix() - 12,
		FiveHour: &Window{UsedPercentage: 23.5, ResetsAt: now.Unix() + 4980},
		SevenDay: &Window{UsedPercentage: 41, ResetsAt: now.Unix() + 302400}}
	if got, want := DeviceUpdateLine(st, now), "U 23.5 4980 41.0 302400 12"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	st.SevenDay = nil
	st.FiveHour.ResetsAt = now.Unix() - 5 // schon zurueckgesetzt
	if got, want := DeviceUpdateLine(st, now), "U 23.5 0 - - 12"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestHookRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	orig := `{
  "model": "opus",
  "statusLine": {"type": "command", "command": "~/.claude/statusline.sh", "padding": 0},
  "permissions": {"allow": ["Bash(ls:*)"]}
}`
	os.WriteFile(path, []byte(orig), 0o644)

	prev, err := InstallHook(path, hookCommand(`C:\Users\Max Mustermann\AppData\Local\TokenOrb\TokenOrb.exe`))
	if err != nil {
		t.Fatal(err)
	}
	if prev != "~/.claude/statusline.sh" {
		t.Errorf("vorheriger Befehl: %q", prev)
	}
	if !IsHooked(path) {
		t.Fatal("nicht eingetragen")
	}
	data, _ := os.ReadFile(path)
	s := string(data)
	// Reihenfolge und andere Schluessel bleiben erhalten
	if !(strings.Index(s, `"model"`) < strings.Index(s, `"statusLine"`) && strings.Index(s, `"statusLine"`) < strings.Index(s, `"permissions"`)) {
		t.Errorf("Reihenfolge veraendert:\n%s", s)
	}
	if !strings.Contains(s, `"C:/Users/Max Mustermann/AppData/Local/TokenOrb/TokenOrb.exe\" statusline`) {
		t.Errorf("Befehl falsch:\n%s", s)
	}
	if _, err := os.Stat(path + ".tokenorb-backup"); err != nil {
		t.Error("keine Sicherung angelegt")
	}
	// zweites Installieren aendert den gemerkten Befehl nicht
	if p2, _ := InstallHook(path, hookCommand(`C:\x\TokenOrb.exe`)); p2 != "" {
		t.Errorf("zweites Install meldet %q", p2)
	}

	if err := RemoveHook(path, prev); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	data, _ = os.ReadFile(path)
	json.Unmarshal(data, &m)
	sl := m["statusLine"].(map[string]any)
	if sl["command"] != "~/.claude/statusline.sh" || sl["padding"] != float64(0) {
		t.Errorf("nicht wiederhergestellt: %v", sl)
	}
	if m["model"] != "opus" || m["permissions"] == nil {
		t.Error("andere Einstellungen verloren")
	}
}

func TestHookFreshFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json") // existiert nicht
	if _, err := InstallHook(path, `"C:/a/TokenOrb.exe" statusline`); err != nil {
		t.Fatal(err)
	}
	if !IsHooked(path) {
		t.Fatal("nicht eingetragen")
	}
	RemoveHook(path, "")
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "statusLine") {
		t.Errorf("statusLine nicht entfernt: %s", data)
	}
}

func TestStatusLineWritesState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TOKENORB_HOME", dir)
	in := `{"model":{"display_name":"Opus"},"rate_limits":{"five_hour":{"used_percentage":50,"resets_at":` +
		jsonInt(time.Now().Unix()+3600) + `}}}`
	var out bytes.Buffer
	runStatusLine(strings.NewReader(in), &out)
	if !strings.Contains(out.String(), "Session 50%") {
		t.Errorf("Ausgabe: %q", out.String())
	}
	st, err := readState(filepath.Join(dir, "usage.json"))
	if err != nil || st.FiveHour == nil || st.FiveHour.UsedPercentage != 50 {
		t.Errorf("Zustand nicht geschrieben: %v %+v", err, st)
	}
	// Aufruf ohne Daten behaelt den letzten Stand
	out.Reset()
	runStatusLine(strings.NewReader(`{"model":{"display_name":"Opus"}}`), &out)
	if !strings.Contains(out.String(), "Session 50%") {
		t.Errorf("letzter Stand verloren: %q", out.String())
	}
}

func TestStatusLineChain(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TOKENORB_HOME", dir)
	saveConfig(Config{ChainStatusLine: "cat >/dev/null; echo eigene-zeile"})
	var out bytes.Buffer
	runStatusLine(strings.NewReader(`{}`), &out)
	if strings.TrimSpace(out.String()) != "eigene-zeile" {
		t.Errorf("Weiterreichen klappt nicht: %q", out.String())
	}
}

func TestIcon(t *testing.T) {
	ico := trayIcon(73, true)
	if len(ico) < 100 || ico[2] != 1 || ico[4] != 3 {
		t.Errorf("ICO-Header kaputt")
	}
}

func jsonInt(v int64) string { b, _ := json.Marshal(v); return string(b) }

func TestParseAccountUsage(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	data := []byte(`{"five_hour":{"utilization":22.0,"resets_at":"2027-01-15T09:39:59.874301+00:00"},"seven_day":{"utilization":21,"resets_at":"2027-01-20T00:59:59+00:00"},"seven_day_opus":null}`)
	st, ok := ParseAccountUsage(data, now)
	if !ok || st.FiveHour == nil || st.SevenDay == nil {
		t.Fatalf("nicht erkannt: %+v", st)
	}
	if st.FiveHour.UsedPercentage != 22 || st.SevenDay.UsedPercentage != 21 {
		t.Fatalf("Prozent falsch: %+v %+v", st.FiveHour, st.SevenDay)
	}
	want := time.Date(2027, 1, 15, 9, 39, 59, 0, time.UTC).Unix()
	if st.FiveHour.ResetsAt != want {
		t.Fatalf("Reset %d, erwartet %d", st.FiveHour.ResetsAt, want)
	}
	if _, ok := ParseAccountUsage([]byte(`{"five_hour":{"resets_at":null}}`), now); ok {
		t.Fatal("leere Antwort darf nicht ok sein")
	}
}
