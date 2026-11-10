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
	sl, exists := currentStatusLine(obj)
	if !exists || !strings.Contains(strings.ToLower(sl.Command), hookMarker) {
		return nil // nicht unserer, nichts anfassen
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
