package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

const appName = "TokenOrb"
const appVersion = "1.4.0"

type Config struct {
	ChainStatusLine string `json:"chainStatusLine"` // vorherige Statuszeile, wird weiter ausgefuehrt
	Button1         string `json:"button1"`         // Aktion fuer Taste 2 kurz am Geraet
	Button2         string `json:"button2"`         // Aktion fuer Taste 2 lang
	UseBluetooth    bool   `json:"useBluetooth"`
	Brightness      int    `json:"brightness"` // 1..100
	LastPort        string `json:"lastPort"`
	AccountMode     bool   `json:"accountMode"`          // Limits direkt vom Konto holen (inoffiziell)
	UpdateRepo      string `json:"updateRepo,omitempty"` // GitHub "besitzer/name", leer = Standard
	NoUpdateCheck   bool   `json:"noUpdateCheck,omitempty"`
	SetupDone       bool   `json:"setupDone,omitempty"` // Ersteinrichtung (Autostart an) erledigt
}

func defaultConfig() Config {
	return Config{
		Button1:      "shift+tab",
		Button2:      "esc",
		UseBluetooth: true,
		Brightness:   100,
	}
}

// appDir: %LOCALAPPDATA%\TokenOrb (bzw. ~/.config/TokenOrb ausserhalb von Windows)
func appDir() string {
	if d := os.Getenv("TOKENORB_HOME"); d != "" {
		return d
	}
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		return filepath.Join(d, appName)
	}
	d, _ := os.UserConfigDir()
	return filepath.Join(d, appName)
}

func configPath() string { return filepath.Join(appDir(), "config.json") }
func statePath() string  { return filepath.Join(appDir(), "usage.json") }
func logPath() string    { return filepath.Join(appDir(), "tokenorb.log") }

func loadConfig() Config {
	cfg := defaultConfig()
	data, err := os.ReadFile(configPath())
	if err == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	if cfg.Brightness < 1 || cfg.Brightness > 100 {
		cfg.Brightness = 100
	}
	return cfg
}

func saveConfig(cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(configPath(), data)
}

// --- gemeinsam genutzte Konfiguration (Tray-App) -----------------------------

var (
	cfgMu  sync.Mutex
	cfgCur *Config
)

func getConfig() Config {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	if cfgCur == nil {
		c := loadConfig()
		cfgCur = &c
	}
	return *cfgCur
}

// reloadConfig verwirft den Zwischenspeicher (z.B. nach Aenderungen aus dem Terminal).
func reloadConfig() {
	cfgMu.Lock()
	cfgCur = nil
	cfgMu.Unlock()
}

func updateConfig(fn func(*Config)) {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	if cfgCur == nil {
		c := loadConfig()
		cfgCur = &c
	}
	fn(cfgCur)
	if err := saveConfig(*cfgCur); err != nil {
		logf("Konfiguration speichern: %v", err)
	}
}
