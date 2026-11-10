//go:build windows

package main

import (
	"errors"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var procMessageBoxW = user32.NewProc("MessageBoxW")

const (
	mbOK          = 0x00000000
	mbYesNo       = 0x00000004
	mbIconInfo    = 0x00000040
	mbIconWarning = 0x00000030
	mbIconQuest   = 0x00000020
	idYes         = 6
)

func messageBox(text string, flags uint32) int {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString(appName)
	r, _, _ := procMessageBoxW.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), uintptr(flags))
	return int(r)
}

var instanceMutex windows.Handle

// acquireSingleInstance: false, wenn TokenOrb schon laeuft.
func acquireSingleInstance() bool {
	name, _ := windows.UTF16PtrFromString(`Local\TokenOrbSingleInstance`)
	h, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return !errors.Is(err, windows.ERROR_ALREADY_EXISTS)
	}
	instanceMutex = h
	return true
}

func isAlreadyRunning() bool {
	name, _ := windows.UTF16PtrFromString(`Local\TokenOrbSingleInstance`)
	h, err := windows.OpenMutex(windows.SYNCHRONIZE, false, name)
	if err != nil {
		return false
	}
	windows.CloseHandle(h)
	return true
}

// ---- Autostart (pro Benutzer, ohne Adminrechte) ----------------------------

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func setAutostart(on bool, exe string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if on {
		return k.SetStringValue(appName, `"`+exe+`"`)
	}
	err = k.DeleteValue(appName)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	return err
}

func isAutostart() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(appName)
	return err == nil
}

func openFolder(dir string) {
	_ = exec.Command("explorer.exe", dir).Start()
}
