package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHookEventsSequence(t *testing.T) {
	st := sessionState{}
	now := time.Unix(1_800_000_000, 0)
	ms := now.UnixMilli()
	step := func(in hookInput) {
		ms += 100
		applyHookEvent(&st, in, ms)
	}
	agg := func() string {
		return aggregateActivity(map[string]sessionState{"a": st}, time.UnixMilli(ms))
	}
	step(hookInput{Event: "SessionStart", Source: "startup"})
	if st.Event != "hello" || agg() != "A idle" {
		t.Fatalf("SessionStart: %+v %s", st, agg())
	}
	step(hookInput{Event: "UserPromptSubmit"})
	if agg() != "A think" {
		t.Fatalf("prompt: %s", agg())
	}
	step(hookInput{Event: "PreToolUse", Tool: "Bash"})
	if agg() != "A tool bash" {
		t.Fatalf("tool: %s", agg())
	}
	step(hookInput{Event: "PermissionRequest", Tool: "Bash"})
	if agg() != "A perm Bash" {
		t.Fatalf("perm: %s", agg())
	}
	step(hookInput{Event: "Notification", NType: "permission_prompt"})
	if agg() != "A perm Bash" {
		t.Fatalf("perm bleibt: %s", agg())
	}
	step(hookInput{Event: "PostToolUse", Tool: "Bash"})
	if agg() != "A think" {
		t.Fatalf("nach Tool: %s", agg())
	}
	step(hookInput{Event: "SubagentStart", AgentID: "x1"})
	step(hookInput{Event: "SubagentStart", AgentID: "x2"})
	if agg() != "A sub 2" {
		t.Fatalf("sub: %s", agg())
	}
	step(hookInput{Event: "SubagentStop", AgentID: "x1"})
	if agg() != "A sub 1" {
		t.Fatalf("sub stop: %s", agg())
	}
	step(hookInput{Event: "PreCompact", Trigger: "auto"})
	step(hookInput{Event: "SubagentStop", AgentID: "x2"})
	if agg() != "A compact" {
		t.Fatalf("compact: %s", agg())
	}
	step(hookInput{Event: "Stop"})
	if agg() != "A idle" || st.Event != "done" {
		t.Fatalf("stop: %s %+v", agg(), st)
	}
	step(hookInput{Event: "Notification", NType: "idle_prompt"})
	if agg() != "A input" {
		t.Fatalf("idle_prompt: %s", agg())
	}
	step(hookInput{Event: "UserPromptSubmit"})
	step(hookInput{Event: "StopFailure", ErrorType: "overloaded_error"})
	if st.Event != "err" || st.EventDetail != "overloaded" {
		t.Fatalf("fehler: %+v", st)
	}
	step(hookInput{Event: "PermissionRequest", Tool: "mcp__github__create_issue"})
	if agg() != "A perm MCP" {
		t.Fatalf("mcp: %s", agg())
	}
	step(hookInput{Event: "SessionEnd"})
	if agg() != "A idle" || st.Event != "bye" {
		t.Fatalf("ende: %s %+v", agg(), st)
	}
}

func TestHookStaleEventDoesNotRevert(t *testing.T) {
	st := sessionState{}
	applyHookEvent(&st, hookInput{Event: "PostToolUse"}, 2000)              // kam zuerst an ...
	applyHookEvent(&st, hookInput{Event: "PreToolUse", Tool: "Read"}, 1000) // ... war aber aelter
	if st.State != "think" {
		t.Fatalf("aelteres Ereignis hat den Zustand ueberschrieben: %+v", st)
	}
}

func TestAggregateSessions(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	ms := now.UnixMilli()
	s := map[string]sessionState{
		"a": {State: "think", Updated: ms - 1000},
		"b": {State: "perm", Detail: "Edit", Updated: ms - 5000},
		"c": {State: "tool", Detail: "web", Updated: ms - 20*60*1000}, // haengt seit 20 min
	}
	if got := aggregateActivity(s, now); got != "A perm Edit" {
		t.Fatalf("got %s", got)
	}
	delete(s, "b")
	if got := aggregateActivity(s, now); got != "A think" {
		t.Fatalf("got %s", got)
	}
	s["a"] = sessionState{State: "think", Updated: ms - 1000, Ended: true}
	if got := aggregateActivity(s, now); got != "A idle" {
		t.Fatalf("beendete Sitzung zaehlt nicht: %s", got)
	}
}

func TestActivityEventsOnce(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	ms := now.UnixMilli()
	s := map[string]sessionState{
		"a": {Event: "done", EventAt: ms - 500},
		"b": {Event: "err", EventDetail: "rate_limit", EventAt: ms - 200},
		"c": {Event: "hello", EventAt: ms - 60000}, // zu alt
	}
	seen := map[string]int64{}
	got := newActivityEvents(s, seen, now)
	if strings.Join(got, "|") != "A done|A err rate_limit" {
		t.Fatalf("got %v", got)
	}
	if again := newActivityEvents(s, seen, now); len(again) != 0 {
		t.Fatalf("doppelt: %v", again)
	}
}

func TestEventHooksInstallKeepsUserHooks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	orig := `{
  "model": "opus",
  "statusLine": {"type": "command", "command": "~/bin/meine-zeile.sh"},
  "hooks": {
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "~/bin/check.sh"}]}],
    "Stop": [{"hooks": [{"type": "command", "command": "say fertig"}]}]
  }
}`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := hookCommand(`C:\Users\x\AppData\Local\TokenOrb\TokenOrb.exe`)
	prev, err := InstallHook(path, cmd)
	if err != nil || prev != "~/bin/meine-zeile.sh" {
		t.Fatalf("install: %v %q", err, prev)
	}
	if _, err := InstallHook(path, cmd); err != nil { // zweimal: keine Duplikate
		t.Fatal(err)
	}
	if !HasEventHooks(path, cmd) {
		t.Fatal("Hooks fehlen")
	}
	var cfg struct {
		Hooks map[string][]hookGroup `json:"hooks"`
	}
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, ev := range hookEvents {
		n := 0
		for _, g := range cfg.Hooks[ev] {
			for _, h := range g.Hooks {
				if h.Command == eventHookCommand(cmd) {
					n++
					if !h.Async {
						t.Errorf("%s nicht async", ev)
					}
				}
			}
		}
		if n != 1 {
			t.Errorf("%s: %d Eintraege", ev, n)
		}
	}
	if len(cfg.Hooks["PreToolUse"]) != 2 || cfg.Hooks["PreToolUse"][0].Hooks[0].Command != "~/bin/check.sh" {
		t.Errorf("eigener PreToolUse-Hook weg: %+v", cfg.Hooks["PreToolUse"])
	}
	if !strings.Contains(string(data), `"model": "opus"`) {
		t.Error("andere Einstellungen verloren")
	}

	if err := RemoveHook(path, prev); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	var after map[string]any
	if err := json.Unmarshal(data, &after); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(data)), "tokenorb") {
		t.Errorf("Reste von uns:\n%s", data)
	}
	h := after["hooks"].(map[string]any)
	if len(h) != 2 || h["PreToolUse"] == nil || h["Stop"] == nil {
		t.Errorf("Nutzer-Hooks falsch wiederhergestellt: %v", h)
	}
	if after["statusLine"].(map[string]any)["command"] != "~/bin/meine-zeile.sh" {
		t.Error("Statuszeile nicht zurueck")
	}
}

func TestEnsureEventHooksUpgrade(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	// Stand nach TokenOrb 1.3.x: nur die Statuszeile
	old := `{"statusLine":{"type":"command","command":"\"/Users/a/Library/Application Support/TokenOrb/TokenOrb.app/Contents/MacOS/TokenOrb\" statusline"}}`
	_ = os.WriteFile(path, []byte(old), 0o644)
	if err := EnsureEventHooks(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `TokenOrb\" hook`) {
		t.Fatalf("Hook nicht eingetragen:\n%s", data)
	}
	before, _ := os.Stat(path)
	time.Sleep(20 * time.Millisecond)
	_ = EnsureEventHooks(path) // schon da -> Datei bleibt
	after, _ := os.Stat(path)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("Datei unnoetig neu geschrieben")
	}
	// fremde Statuszeile: nichts anfassen
	_ = os.WriteFile(path, []byte(`{"statusLine":{"type":"command","command":"ccusage"}}`), 0o644)
	_ = EnsureEventHooks(path)
	data, _ = os.ReadFile(path)
	if strings.Contains(string(data), "hook") {
		t.Error("fremde Einstellungen veraendert")
	}
}

func TestRunHookWritesSession(t *testing.T) {
	t.Setenv("TOKENORB_HOME", t.TempDir())
	runHook(strings.NewReader(`{"session_id":"abc-123","hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"x"}}`))
	s := loadSessions(sessionsDir())
	if st, ok := s["abc-123"]; !ok || st.State != "tool" || st.Detail != "edit" {
		t.Fatalf("got %+v", s)
	}
	runHook(strings.NewReader(`kaputt`)) // darf nicht abstuerzen
	if line := timeLine(time.Unix(1_800_000_000, 0).In(time.FixedZone("CET", 3600))); line != "T 1800000000 60" {
		t.Fatalf("Zeit: %s", line)
	}
}
