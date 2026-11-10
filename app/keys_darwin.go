//go:build darwin

package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// Tasten am Geraet -> Tastendruck am Mac ueber AppleScript (System Events).
// macOS fragt beim ersten Mal nach der Bedienungshilfen-Erlaubnis.

var macKeyCodes = map[string]int{
	"tab": 48, "esc": 53, "enter": 36, "space": 49, "backspace": 51, "delete": 117,
	"up": 126, "down": 125, "left": 123, "right": 124,
	"home": 115, "end": 119, "pageup": 116, "pagedown": 121,
	"f1": 122, "f2": 120, "f3": 99, "f4": 118, "f5": 96, "f6": 97, "f7": 98, "f8": 100,
	"f9": 101, "f10": 109, "f11": 103, "f12": 111,
}

var macModifiers = map[string]string{
	"shift": "shift down", "ctrl": "control down", "strg": "control down",
	"alt": "option down", "option": "option down", "cmd": "command down", "win": "command down",
}

// appleScriptFor baut z.B. aus "shift+tab" -> tell application "System Events" to key code 48 using {shift down}
func appleScriptFor(combo string) (string, error) {
	var mods []string
	key := ""
	for _, part := range strings.Split(strings.ToLower(combo), "+") {
		part = strings.TrimSpace(part)
		if m, ok := macModifiers[part]; ok {
			mods = append(mods, m)
			continue
		}
		if key != "" {
			return "", fmt.Errorf("mehr als eine Taste in %q", combo)
		}
		key = part
	}
	var action string
	if code, ok := macKeyCodes[key]; ok {
		action = fmt.Sprintf("key code %d", code)
	} else if len(key) == 1 && ((key[0] >= 'a' && key[0] <= 'z') || (key[0] >= '0' && key[0] <= '9')) {
		action = fmt.Sprintf("keystroke %q", key)
	} else {
		return "", fmt.Errorf("unbekannte Taste %q", key)
	}
	if len(mods) > 0 {
		action += " using {" + strings.Join(mods, ", ") + "}"
	}
	return `tell application "System Events" to ` + action, nil
}

func sendCombo(combo string) error {
	combo = strings.TrimSpace(combo)
	if combo == "" || strings.EqualFold(combo, "none") {
		return nil
	}
	script, err := appleScriptFor(combo)
	if err != nil {
		return err
	}
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("osascript: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
