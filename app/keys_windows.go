//go:build windows

package main

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Tasten am Geraet -> Tastendruck am PC (z.B. Shift+Tab = Modus in Claude Code wechseln).

var (
	user32        = windows.NewLazySystemDLL("user32.dll")
	procSendInput = user32.NewProc("SendInput")
)

const (
	inputKeyboard  = 1
	keyeventfKeyUp = 0x0002
	keyeventfExt   = 0x0001
)

type keybdInput struct {
	wVk         uint16
	wScan       uint16
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

// entspricht INPUT (64 Bit: 40 Bytes)
type input struct {
	typ uint32
	_   uint32
	ki  keybdInput
	_   [8]byte
}

var vkNames = map[string]uint16{
	"shift": 0x10, "ctrl": 0x11, "strg": 0x11, "alt": 0x12, "win": 0x5B,
	"tab": 0x09, "esc": 0x1B, "enter": 0x0D, "space": 0x20, "backspace": 0x08,
	"up": 0x26, "down": 0x28, "left": 0x25, "right": 0x27,
	"home": 0x24, "end": 0x23, "pageup": 0x21, "pagedown": 0x22, "delete": 0x2E,
}

var extendedKeys = map[uint16]bool{0x26: true, 0x28: true, 0x25: true, 0x27: true, 0x24: true, 0x23: true, 0x21: true, 0x22: true, 0x2E: true, 0x5B: true}

func vkFor(name string) (uint16, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if v, ok := vkNames[name]; ok {
		return v, true
	}
	if len(name) == 1 {
		c := name[0]
		if c >= 'a' && c <= 'z' {
			return uint16(c - 'a' + 'A'), true
		}
		if c >= '0' && c <= '9' {
			return uint16(c), true
		}
	}
	var n int
	if _, err := fmt.Sscanf(name, "f%d", &n); err == nil && n >= 1 && n <= 24 {
		return uint16(0x70 + n - 1), true
	}
	return 0, false
}

// sendCombo drueckt z.B. "shift+tab" oder "ctrl+c".
func sendCombo(combo string) error {
	combo = strings.TrimSpace(combo)
	if combo == "" || strings.EqualFold(combo, "none") {
		return nil
	}
	var keys []uint16
	for _, part := range strings.Split(combo, "+") {
		vk, ok := vkFor(part)
		if !ok {
			return fmt.Errorf("unbekannte Taste %q", part)
		}
		keys = append(keys, vk)
	}
	var in []input
	mk := func(vk uint16, up bool) input {
		var f uint32
		if up {
			f |= keyeventfKeyUp
		}
		if extendedKeys[vk] {
			f |= keyeventfExt
		}
		return input{typ: inputKeyboard, ki: keybdInput{wVk: vk, dwFlags: f}}
	}
	for _, k := range keys {
		in = append(in, mk(k, false))
	}
	for i := len(keys) - 1; i >= 0; i-- {
		in = append(in, mk(keys[i], true))
	}
	n, _, err := procSendInput.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(in[0]))
	if int(n) != len(in) {
		return fmt.Errorf("SendInput: %v", err)
	}
	return nil
}
