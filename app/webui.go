//go:build windows || darwin

package main

// Einstellungsfenster: kleine Webseite, die die App nur auf 127.0.0.1 anbietet.
// Sie wird als eigenes App-Fenster geoeffnet (Edge/Chrome im App-Modus, sonst Browser).

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const uiAddr = "127.0.0.1:47123"
const uiURL = "http://" + uiAddr + "/"

//go:embed webui.html
var uiHTML []byte

var (
	uiMu   sync.Mutex
	uiDemo bool
)

type uiWin struct {
	Pct      float64 `json:"pct"`
	ResetsAt int64   `json:"resets_at"`
}

type uiStatus struct {
	OS          string       `json:"os"` // "windows" | "mac"
	Version     string       `json:"version"`
	Device      string       `json:"device"`
	Connected   bool         `json:"connected"`
	Paused      bool         `json:"paused"`
	Hooked      bool         `json:"hooked"`
	Account     bool         `json:"account"`
	AccountInfo string       `json:"accountInfo"`
	Demo        bool         `json:"demo"`
	Updated     int64        `json:"updated"`
	H5          *uiWin       `json:"h5"`
	D7          *uiWin       `json:"d7"`
	Brightness  int          `json:"brightness"`
	Button1     string       `json:"button1"`
	Button2     string       `json:"button2"`
	Actions     [][2]string  `json:"actions"`
	Autostart   bool         `json:"autostart"`
	Bluetooth   bool         `json:"bluetooth"`
	AutoCheck   bool         `json:"autoCheck"`
	Update      updateStatus `json:"update"`
	Now         int64        `json:"now"`
}

func buildStatus(dm *deviceManager) uiStatus {
	cfg := getConfig()
	id, usb := dm.Device()
	st := uiStatus{
		OS: platformName(), Version: appVersion, Device: dm.Status(), Paused: dm.Paused(),
		Hooked: IsHooked(claudeSettingsPath()), Account: cfg.AccountMode, AccountInfo: account.Status(),
		Brightness: cfg.Brightness, Button1: cfg.Button1, Button2: cfg.Button2, Actions: keyActions,
		Bluetooth: cfg.UseBluetooth, AutoCheck: !cfg.NoUpdateCheck, Now: time.Now().Unix(),
		Update: upd.Status(id, usb),
	}
	st.Connected = id != ""
	st.Autostart = autostartEnabled()
	uiMu.Lock()
	st.Demo = uiDemo
	uiMu.Unlock()
	if u, err := readState(statePath()); err == nil {
		st.Updated = u.UpdatedAt
		if u.FiveHour != nil {
			st.H5 = &uiWin{u.FiveHour.UsedPercentage, u.FiveHour.ResetsAt}
		}
		if u.SevenDay != nil {
			st.D7 = &uiWin{u.SevenDay.UsedPercentage, u.SevenDay.ResetsAt}
		}
	}
	return st
}

// nur Anfragen von der eigenen Seite annehmen (Schutz gegen fremde Webseiten)
func uiAllowed(r *http.Request) bool {
	if r.Host != uiAddr && r.Host != "localhost:47123" {
		return false
	}
	if r.Method == http.MethodPost {
		if r.Header.Get("X-TokenOrb") != "1" {
			return false
		}
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+uiAddr && o != "http://localhost:47123" {
			return false
		}
	}
	return true
}

func uiUp() bool {
	c, err := net.DialTimeout("tcp", uiAddr, 300*time.Millisecond)
	if err == nil {
		c.Close()
	}
	return err == nil
}

func startWebUI(dm *deviceManager) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !uiAllowed(r) || r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(uiHTML)
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		if !uiAllowed(r) {
			http.Error(w, "nein", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(buildStatus(dm))
	})
	mux.HandleFunc("/api/set", func(w http.ResponseWriter, r *http.Request) {
		if !uiAllowed(r) || r.Method != http.MethodPost {
			http.Error(w, "nein", http.StatusForbidden)
			return
		}
		var req struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil {
			http.Error(w, "kaputt", http.StatusBadRequest)
			return
		}
		if err := applySetting(dm, req.Key, req.Value); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(buildStatus(dm))
	})
	go func() {
		ln, err := net.Listen("tcp", uiAddr)
		if err != nil {
			logf("Einstellungsfenster: %v", err)
			return
		}
		_ = http.Serve(ln, mux)
	}()
}

func asBool(v any) bool { b, _ := v.(bool); return b }

func applySetting(dm *deviceManager, key string, v any) error {
	switch key {
	case "brightness":
		f, ok := v.(float64)
		if !ok || f < 1 || f > 100 {
			return fmt.Errorf("Helligkeit 1..100")
		}
		updateConfig(func(c *Config) { c.Brightness = int(f) })
		dm.Command(fmt.Sprintf("B %d", int(f)))
	case "demo":
		on := asBool(v)
		uiMu.Lock()
		uiDemo = on
		uiMu.Unlock()
		if on {
			dm.Command("D 1")
		} else {
			dm.Command("D 0")
		}
	case "account":
		on := asBool(v)
		updateConfig(func(c *Config) { c.AccountMode = on })
	case "hook":
		path := claudeSettingsPath()
		if asBool(v) {
			prev, err := InstallHook(path, hookCommand(installedExePath()))
			if err != nil {
				return err
			}
			if prev != "" {
				updateConfig(func(c *Config) { c.ChainStatusLine = prev })
			}
		} else if err := RemoveHook(path, getConfig().ChainStatusLine); err != nil {
			return err
		}
	case "button1", "button2":
		s, _ := v.(string)
		if s != "none" {
			if err := validateCombo(s); err != nil {
				return err
			}
		}
		updateConfig(func(c *Config) {
			if key == "button1" {
				c.Button1 = s
			} else {
				c.Button2 = s
			}
		})
	case "bluetooth":
		on := asBool(v)
		updateConfig(func(c *Config) { c.UseBluetooth = on })
	case "autostart":
		return setAutostartEnabled(asBool(v))
	case "autocheck":
		on := asBool(v)
		updateConfig(func(c *Config) { c.NoUpdateCheck = !on })
	case "pause":
		dm.SetPaused(asBool(v))
	case "check":
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		_ = upd.Check(ctx, updateRepo()) // Fehler steht im Status
	case "updateRepo":
		s, _ := v.(string)
		s = strings.Trim(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(s), "https://github.com/"), "github.com/")), "/")
		updateConfig(func(c *Config) { c.UpdateRepo = s })
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		_ = upd.Check(ctx, updateRepo())
	case "updateApp":
		return upd.StartAppUpdate()
	case "updateFirmware":
		return upd.StartFirmwareUpdate(dm)
	default:
		return platformSetting(key, v)
	}
	return nil
}
