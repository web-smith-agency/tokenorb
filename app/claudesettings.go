package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Claude Code liest ~/.claude/settings.json. Wir tragen dort unsere EXE als
// "statusLine"-Befehl ein. Eine vorhandene Statuszeile wird nicht ersetzt,
// sondern gemerkt und weiter ausgefuehrt (siehe statusline.go).

const hookMarker = "tokenorb"

func claudeSettingsPath() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "settings.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

// orderedObject haelt die Schluessel in Originalreihenfolge, damit wir die
// Datei des Nutzers nicht unnoetig umsortieren.
type orderedObject struct {
	keys []string
	vals map[string]json.RawMessage
}

func parseOrderedObject(data []byte) (*orderedObject, error) {
	obj := &orderedObject{vals: map[string]json.RawMessage{}}
	if len(bytes.TrimSpace(data)) == 0 {
		return obj, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("settings.json ist kein JSON-Objekt")
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := t.(string)
		if !ok {
			return nil, errors.New("ungueltiger Schluessel in settings.json")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		if _, exists := obj.vals[key]; !exists {
			obj.keys = append(obj.keys, key)
		}
		obj.vals[key] = raw
	}
	return obj, nil
}

func (o *orderedObject) set(key string, val json.RawMessage) {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = val
}

func (o *orderedObject) remove(key string) {
	if _, ok := o.vals[key]; !ok {
		return
	}
	delete(o.vals, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

// marshalCompact: wie marshal, aber ohne Einrueckung (fuer verschachtelte Objekte)
func (o *orderedObject) marshalCompact() (json.RawMessage, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(o.vals[k])
	}
	buf.WriteByte('}')
	return json.RawMessage(buf.Bytes()), nil
}

func (o *orderedObject) marshal() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(o.vals[k])
	}
	buf.WriteByte('}')
	var out bytes.Buffer
	if err := json.Indent(&out, buf.Bytes(), "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

type statusLineSetting struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Padding *int   `json:"padding,omitempty"`
}

func readSettings(path string) (*orderedObject, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return parseOrderedObject(nil)
	}
	if err != nil {
		return nil, err
	}
	return parseOrderedObject(data)
}

func currentStatusLine(obj *orderedObject) (statusLineSetting, bool) {
	var sl statusLineSetting
	raw, ok := obj.vals["statusLine"]
	if !ok {
		return sl, false
	}
	if json.Unmarshal(raw, &sl) != nil {
		return sl, false
	}
	return sl, true
}

// IsHooked: ist unsere EXE als Statuszeile eingetragen?
func IsHooked(path string) bool {
	obj, err := readSettings(path)
	if err != nil {
		return false
	}
	sl, ok := currentStatusLine(obj)
	return ok && strings.Contains(strings.ToLower(sl.Command), hookMarker)
}

// InstallHook traegt ourCommand ein. Rueckgabe: der bisherige Befehl (zum
// Weiterreichen), leer wenn es keinen gab.
func InstallHook(path, ourCommand string) (previous string, err error) {
	obj, err := readSettings(path)
	if err != nil {
		return "", fmt.Errorf("settings.json lesen: %w", err)
	}
	sl, exists := currentStatusLine(obj)
	if exists && strings.Contains(strings.ToLower(sl.Command), hookMarker) {
		// schon eingetragen – nur Befehl aktualisieren (z.B. neuer Pfad)
		previous = ""
	} else if exists && sl.Type == "command" {
		previous = sl.Command
	}

	// einmalige Sicherung der Originaldatei
	if data, rerr := os.ReadFile(path); rerr == nil {
		backup := path + ".tokenorb-backup"
		if _, serr := os.Stat(backup); errors.Is(serr, os.ErrNotExist) {
			_ = os.WriteFile(backup, data, 0o644)
		}
	}

	nsl := statusLineSetting{Type: "command", Command: ourCommand}
	if exists && sl.Padding != nil {
		nsl.Padding = sl.Padding
	}
	raw, _ := json.Marshal(nsl)
	obj.set("statusLine", raw)
	if err := setEventHooks(obj, eventHookCommand(ourCommand)); err != nil {
		return "", err
	}
	out, err := obj.marshal()
	if err != nil {
		return "", err
	}
	return previous, writeFileAtomic(path, out)
}

// RemoveHook stellt den vorherigen Zustand wieder her.
func RemoveHook(path, previous string) error {
	obj, err := readSettings(path)
	if err != nil {
		return err
	}
	hooksChanged, err := removeEventHooks(obj)
	if err != nil {
		return err
	}
	sl, exists := currentStatusLine(obj)
	if !exists || !strings.Contains(strings.ToLower(sl.Command), hookMarker) {
		if !hooksChanged {
			return nil // nicht unserer, nichts anfassen
		}
		out, err := obj.marshal()
		if err != nil {
			return err
		}
		return writeFileAtomic(path, out)
	}
	if previous != "" {
		raw, _ := json.Marshal(statusLineSetting{Type: "command", Command: previous, Padding: sl.Padding})
		obj.set("statusLine", raw)
	} else {
		obj.remove("statusLine")
	}
	out, err := obj.marshal()
	if err != nil {
		return err
	}
	return writeFileAtomic(path, out)
}

// hookCommand baut den Befehl so, dass er in Git Bash und cmd funktioniert.
func hookCommand(exePath string) string {
	p := strings.ReplaceAll(exePath, `\`, "/")
	return fmt.Sprintf(`"%s" statusline`, p)
}

// =====================================================================
// Hooks fuer die Aktivitaetsanzeige (ab 1.4.0): Claude Code ruft bei diesen
// Ereignissen "<exe> hook" auf. Alle laufen im Hintergrund ("async"), Claude
// wartet also nie auf TokenOrb. Eigene Hooks des Nutzers bleiben unangetastet.
// =====================================================================

var hookEvents = []string{
	"SessionStart", "SessionEnd", "UserPromptSubmit",
	"PreToolUse", "PostToolUse", "PostToolUseFailure",
	"PermissionRequest", "PermissionDenied", "Notification",
	"SubagentStart", "SubagentStop", "PreCompact", "PostCompact",
	"Stop", "StopFailure",
}

// Diese drei laufen synchron (dauert ca. 20 ms): Bei "claude -p" und in Werkzeugen, die
// Claude Code ohne Terminal starten, beendet Claude Code Hintergrund-Hooks sofort nach dem
// Ende – dann fehlte "fertig" und der Puck bliebe auf "denkt nach" stehen.
var syncHookEvents = map[string]bool{"Stop": true, "StopFailure": true, "SessionEnd": true}

type hookHandler struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Async   bool   `json:"async,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

type hookGroup struct {
	Matcher *string       `json:"matcher,omitempty"`
	Hooks   []hookHandler `json:"hooks"`
}

// eventHookCommand macht aus dem Statuszeilen-Befehl den Hook-Befehl
func eventHookCommand(statusCmd string) string {
	return strings.TrimSuffix(statusCmd, " statusline") + " hook"
}

func isOurHookGroup(raw json.RawMessage) bool {
	var g hookGroup
	if json.Unmarshal(raw, &g) != nil || len(g.Hooks) == 0 {
		return false
	}
	for _, h := range g.Hooks {
		c := strings.ToLower(h.Command)
		if !strings.Contains(c, hookMarker) || !strings.HasSuffix(strings.TrimSpace(c), " hook") {
			return false
		}
	}
	return true
}

func readHooksObject(obj *orderedObject) (*orderedObject, error) {
	raw, ok := obj.vals["hooks"]
	if !ok {
		return parseOrderedObject(nil)
	}
	h, err := parseOrderedObject(raw)
	if err != nil {
		return nil, fmt.Errorf("\"hooks\" in settings.json: %w", err)
	}
	return h, nil
}

// withoutOurGroups entfernt unsere Eintraege aus einem Ereignis-Array
func withoutOurGroups(raw json.RawMessage) ([]json.RawMessage, bool, error) {
	var groups []json.RawMessage
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil, false, err
	}
	keep := groups[:0]
	removed := false
	for _, g := range groups {
		if isOurHookGroup(g) {
			removed = true
			continue
		}
		keep = append(keep, g)
	}
	return keep, removed, nil
}

func setEventHooks(obj *orderedObject, cmd string) error {
	hooks, err := readHooksObject(obj)
	if err != nil {
		return err
	}
	for _, ev := range hookEvents {
		h := hookHandler{Type: "command", Command: cmd, Async: true, Timeout: 10}
		if syncHookEvents[ev] {
			h.Async, h.Timeout = false, 5
		}
		ours, _ := json.Marshal(hookGroup{Hooks: []hookHandler{h}})
		var groups []json.RawMessage
		if raw, ok := hooks.vals[ev]; ok {
			g, _, err := withoutOurGroups(raw)
			if err != nil {
				return fmt.Errorf("hooks.%s in settings.json: %w", ev, err)
			}
			groups = g
		}
		groups = append(groups, ours)
		arr, _ := json.Marshal(groups)
		hooks.set(ev, arr)
	}
	out, err := hooks.marshalCompact()
	if err != nil {
		return err
	}
	obj.set("hooks", out)
	return nil
}

func removeEventHooks(obj *orderedObject) (bool, error) {
	if _, ok := obj.vals["hooks"]; !ok {
		return false, nil
	}
	hooks, err := readHooksObject(obj)
	if err != nil {
		return false, err
	}
	changed := false
	for _, ev := range append([]string(nil), hooks.keys...) {
		groups, removed, err := withoutOurGroups(hooks.vals[ev])
		if err != nil || !removed {
			continue
		}
		changed = true
		if len(groups) == 0 {
			hooks.remove(ev)
		} else {
			arr, _ := json.Marshal(groups)
			hooks.set(ev, arr)
		}
	}
	if !changed {
		return false, nil
	}
	if len(hooks.keys) == 0 {
		obj.remove("hooks")
	} else {
		out, err := hooks.marshalCompact()
		if err != nil {
			return false, err
		}
		obj.set("hooks", out)
	}
	return true, nil
}

// HasEventHooks: sind unsere Aktivitaets-Hooks mit genau diesem Befehl eingetragen?
func HasEventHooks(path, statusCmd string) bool {
	obj, err := readSettings(path)
	if err != nil {
		return false
	}
	hooks, err := readHooksObject(obj)
	if err != nil {
		return false
	}
	want := eventHookCommand(statusCmd)
	for _, ev := range hookEvents {
		var groups []hookGroup
		if json.Unmarshal(hooks.vals[ev], &groups) != nil {
			return false
		}
		found := false
		for _, g := range groups {
			for _, h := range g.Hooks {
				if h.Command == want && h.Async == !syncHookEvents[ev] {
					found = true
				}
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// EnsureEventHooks traegt die Aktivitaets-Hooks nach, wenn die Statuszeile schon
// verbunden ist (Update von 1.3.x). Aendert sonst nichts.
// Der Befehl wird aus der eingetragenen Statuszeile abgeleitet (gleicher Programmpfad).
func EnsureEventHooks(path string) error {
	obj, err := readSettings(path)
	if err != nil {
		return err
	}
	sl, ok := currentStatusLine(obj)
	if !ok || !strings.Contains(strings.ToLower(sl.Command), hookMarker) || !strings.HasSuffix(sl.Command, " statusline") {
		return nil
	}
	statusCmd := sl.Command
	if HasEventHooks(path, statusCmd) {
		return nil
	}
	if err := setEventHooks(obj, eventHookCommand(statusCmd)); err != nil {
		return err
	}
	out, err := obj.marshal()
	if err != nil {
		return err
	}
	logf("Aktivitaets-Hooks in %s eingetragen", path)
	return writeFileAtomic(path, out)
}
