package main

// Claude-Aktivitaet: Claude Code ruft bei bestimmten Ereignissen (Hooks) "TokenOrb hook" auf
// und schickt JSON auf stdin. Wir merken uns pro Sitzung den Zustand in
// <appDir>/sitzungen/<session_id>.json. Die laufende App fasst alle Sitzungen zusammen und
// schickt dem Geraet eine Zeile "A <zustand> [arg]" (siehe firmware/include/comms.h).

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Zustaende, nach Wichtigkeit sortiert (hoeher gewinnt bei mehreren Sitzungen)
var actPriority = map[string]int{"idle": 0, "input": 1, "think": 2, "tool": 3, "sub": 4, "compact": 5, "perm": 6}

type sessionState struct {
	State       string           `json:"state"`            // idle|think|tool|perm|input|compact
	Detail      string           `json:"detail,omitempty"` // Werkzeug-Art bzw. Werkzeugname bei perm
	Subs        map[string]int64 `json:"subs,omitempty"`   // laufende Helfer: agent_id -> Start (ms)
	Event       string           `json:"event,omitempty"`  // letztes kurzes Ereignis: done|err|hello|bye
	EventDetail string           `json:"eventDetail,omitempty"`
	EventAt     int64            `json:"eventAt,omitempty"` // ms
	Updated     int64            `json:"updated"`           // ms, letzter Zustandswechsel
	Ended       bool             `json:"ended,omitempty"`
}

type hookInput struct {
	SessionID string `json:"session_id"`
	Event     string `json:"hook_event_name"`
	Tool      string `json:"tool_name"`
	NType     string `json:"notification_type"`
	Message   string `json:"message"`
	AgentID   string `json:"agent_id"`
	Source    string `json:"source"`
	Trigger   string `json:"trigger"`
	Error     any    `json:"error"`
	ErrorType string `json:"error_type"`
}

func sessionsDir() string { return filepath.Join(appDir(), "sitzungen") }

// toolKind fasst Werkzeuge grob zusammen (nur fuer die LEDs/Anzeige)
func toolKind(name string) string {
	switch {
	case name == "Bash" || name == "BashOutput" || name == "KillShell" || name == "PowerShell":
		return "bash"
	case name == "Edit" || name == "Write" || name == "MultiEdit" || name == "NotebookEdit":
		return "edit"
	case name == "Read" || name == "Grep" || name == "Glob" || name == "LS":
		return "read"
	case name == "WebSearch" || name == "WebFetch":
		return "web"
	case name == "Task" || name == "Agent":
		return "task"
	}
	return "other"
}

// kurzer, ASCII-sicherer Werkzeugname fuer "... erlauben?" auf dem Display
func shortToolName(name string) string {
	if strings.HasPrefix(name, "mcp__") {
		return "MCP"
	}
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
		if b.Len() >= 12 {
			break
		}
	}
	if b.Len() == 0 {
		return "Werkzeug"
	}
	return b.String()
}

func errorKind(in hookInput) string {
	cands := []string{in.ErrorType}
	switch e := in.Error.(type) {
	case string:
		cands = append(cands, e)
	case map[string]any:
		for _, k := range []string{"type", "error_type", "code"} {
			if s, ok := e[k].(string); ok {
				cands = append(cands, s)
			}
		}
	}
	for _, c := range cands {
		c = strings.ToLower(strings.TrimSpace(c))
		for _, k := range []string{"rate_limit", "overloaded", "server_error", "authentication_failed", "billing_error"} {
			if strings.Contains(c, k) {
				return k
			}
		}
	}
	return "unknown"
}

// applyHookEvent veraendert den Sitzungszustand fuer ein Hook-Ereignis (now in ms).
func applyHookEvent(st *sessionState, in hookInput, now int64) {
	// aeltere Ereignisse (Hooks laufen parallel) duerfen den Zustand nicht zuruecksetzen
	stale := st.Updated > now
	// Hooks starten als eigene Prozesse und koennen sich ueberholen (z.B. beim ersten Start
	// nach einem Update, wenn macOS das Programm noch prueft). Ein "arbeitet"-Ereignis, das
	// direkt nach "fertig" ankommt, gehoert fast immer noch zur gerade beendeten Runde.
	lateAfterStop := st.State == "idle" && st.Event == "done" && now-st.EventAt >= 0 && now-st.EventAt < 1500
	setState := func(s, detail string) {
		if stale {
			return
		}
		st.State, st.Detail, st.Updated = s, detail, now
	}
	event := func(e, detail string) {
		st.Event, st.EventDetail, st.EventAt = e, detail, now
	}

	switch in.Event {
	case "SessionStart":
		st.Ended = false
		st.Subs = nil
		setState("idle", "")
		if in.Source == "" || in.Source == "startup" || in.Source == "resume" {
			event("hello", "")
		}
	case "UserPromptSubmit":
		if !lateAfterStop {
			setState("think", "")
		}
	case "PreToolUse":
		if !lateAfterStop {
			setState("tool", toolKind(in.Tool))
		}
	case "PostToolUse", "PostToolUseFailure", "PermissionDenied":
		if !lateAfterStop {
			setState("think", "")
		}
	case "PermissionRequest":
		setState("perm", shortToolName(in.Tool))
	case "Notification":
		nt := strings.ToLower(in.NType)
		msg := strings.ToLower(in.Message)
		switch {
		case nt == "permission_prompt" || (nt == "" && strings.Contains(msg, "permission")):
			if st.State != "perm" { // PermissionRequest kam nicht (aeltere Claude-Code-Version)
				setState("perm", "")
			}
		case nt == "idle_prompt" || (nt == "" && strings.Contains(msg, "waiting for your input")):
			// Claude wartet auf dich -> arbeitet sicher nicht mehr (z.B. nach Abbruch mit Esc,
			// dann kommt kein Stop-Ereignis)
			if st.State != "perm" {
				st.Subs = nil
				setState("input", "")
			}
		}
	case "SubagentStart":
		if st.Subs == nil {
			st.Subs = map[string]int64{}
		}
		id := in.AgentID
		if id == "" {
			id = fmt.Sprintf("x%d", now)
		}
		st.Subs[id] = now
	case "SubagentStop":
		if in.AgentID != "" {
			delete(st.Subs, in.AgentID)
		} else {
			// ohne ID: den aeltesten entfernen
			oldest, ok := "", int64(0)
			for k, v := range st.Subs {
				if oldest == "" || v < ok {
					oldest, ok = k, v
				}
			}
			delete(st.Subs, oldest)
		}
	case "PreCompact":
		setState("compact", "")
	case "PostCompact":
		if in.Trigger == "manual" {
			setState("idle", "")
		} else {
			setState("think", "")
		}
	case "Stop":
		st.Subs = nil
		setState("idle", "")
		event("done", "")
	case "StopFailure":
		st.Subs = nil
		setState("idle", "")
		event("err", errorKind(in))
	case "SessionEnd":
		st.Subs = nil
		st.Ended = true
		setState("idle", "")
		event("bye", "")
	}
}

// ---- Dateien -------------------------------------------------------------------

func safeSessionName(id string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
		if b.Len() >= 64 {
			break
		}
	}
	if b.Len() == 0 {
		return "ohne-id"
	}
	return b.String()
}

// withFileLock: einfache Sperre ueber eine .lock-Datei (Hooks laufen parallel)
func withFileLock(path string, fn func()) {
	lock := path + ".lock"
	deadline := time.Now().Add(400 * time.Millisecond)
	for {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			defer os.Remove(lock)
			fn()
			return
		}
		if fi, serr := os.Stat(lock); serr == nil && time.Since(fi.ModTime()) > 3*time.Second {
			_ = os.Remove(lock) // haengengebliebene Sperre
			continue
		}
		if time.Now().After(deadline) {
			fn() // lieber ohne Sperre als gar nicht
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func readSession(path string) (sessionState, error) {
	var st sessionState
	data, err := os.ReadFile(path)
	if err != nil {
		return st, err
	}
	err = json.Unmarshal(data, &st)
	return st, err
}

// runHook wird von Claude Code fuer jedes eingetragene Ereignis aufgerufen (Modus "hook").
// Muss schnell sein, gibt nichts aus und scheitert leise.
func runHook(stdin io.Reader) {
	start := time.Now().UnixMilli()
	data, _ := io.ReadAll(io.LimitReader(stdin, 1<<20))
	var in hookInput
	if json.Unmarshal(data, &in) != nil || in.Event == "" {
		return
	}
	dir := sessionsDir()
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	path := filepath.Join(dir, safeSessionName(in.SessionID)+".json")
	withFileLock(path, func() {
		st, err := readSession(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			st = sessionState{}
		}
		applyHookEvent(&st, in, start)
		if out, err := json.Marshal(st); err == nil {
			_ = writeFileAtomic(path, out)
		}
	})
	if rand.Intn(40) == 0 {
		cleanupSessions(dir, time.Now())
	}
}

// alte Sitzungsdateien wegraeumen
func cleanupSessions(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && now.Sub(fi.ModTime()) > 24*time.Hour {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func loadSessions(dir string) map[string]sessionState {
	out := map[string]sessionState{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		if st, err := readSession(filepath.Join(dir, name)); err == nil {
			out[strings.TrimSuffix(name, ".json")] = st
		}
	}
	return out
}

// ---- Zusammenfassen ----------------------------------------------------------------

const (
	actWorkTimeout = 10 * time.Minute // arbeitet ohne neues Ereignis -> vermutlich abgebrochen
	actWaitTimeout = 60 * time.Minute // wartet auf Erlaubnis / Eingabe
	actSubTimeout  = 30 * time.Minute
	actEventMaxAge = 8 * time.Second // nur frische Ereignisse abspielen
)

// aggregateActivity: wichtigster Zustand ueber alle Sitzungen als Geraete-Zeile.
func aggregateActivity(sessions map[string]sessionState, now time.Time) string {
	nowMs := now.UnixMilli()
	best, bestDetail, subs := "idle", "", 0
	// feste Reihenfolge, damit gleiche Prioritaet stabil bleibt
	keys := make([]string, 0, len(sessions))
	for k := range sessions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		st := sessions[k]
		if st.Ended {
			continue
		}
		age := time.Duration(nowMs-st.Updated) * time.Millisecond
		state := st.State
		switch state {
		case "think", "tool", "compact":
			if age > actWorkTimeout {
				state = "idle"
			}
		case "perm", "input":
			if age > actWaitTimeout {
				state = "idle"
			}
		default:
			state = "idle"
		}
		for _, startMs := range st.Subs {
			if time.Duration(nowMs-startMs)*time.Millisecond < actSubTimeout {
				subs++
			}
		}
		if actPriority[state] > actPriority[best] {
			best, bestDetail = state, st.Detail
		}
	}
	if subs > 0 && actPriority["sub"] > actPriority[best] {
		return fmt.Sprintf("A sub %d", subs)
	}
	switch best {
	case "tool":
		if bestDetail == "" {
			bestDetail = "other"
		}
		return "A tool " + bestDetail
	case "perm":
		if bestDetail != "" {
			return "A perm " + bestDetail
		}
		return "A perm"
	}
	return "A " + best
}

// newActivityEvents liefert neue kurze Ereignisse (Fertig, Fehler, Hallo, Tschuess) als
// Geraete-Zeilen und merkt sich in seen, was schon geschickt wurde.
func newActivityEvents(sessions map[string]sessionState, seen map[string]int64, now time.Time) []string {
	var out []string
	keys := make([]string, 0, len(sessions))
	for k := range sessions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		st := sessions[k]
		if st.EventAt == 0 || st.EventAt <= seen[k] {
			continue
		}
		seen[k] = st.EventAt
		if time.Duration(now.UnixMilli()-st.EventAt)*time.Millisecond > actEventMaxAge {
			continue // zu alt (z.B. App gerade gestartet)
		}
		switch st.Event {
		case "err":
			out = append(out, "A err "+st.EventDetail)
		case "done", "hello", "bye":
			out = append(out, "A "+st.Event)
		}
	}
	return out
}

// timeLine: Uhrzeit fuers Geraet (Nachtmodus)
func timeLine(now time.Time) string {
	_, off := now.Zone()
	return fmt.Sprintf("T %d %d", now.Unix(), off/60)
}
