package main

// Updates fuer App und Puck-Firmware aus GitHub Releases.
//
// Ein Release enthaelt:
//   manifest.json                          {"app":"1.3.0","firmware":"1.2.0","notes":"...",
//                                           "files":{"<name>":{"sha256":"...","size":123}}}
//   TokenOrb-Setup-<ver>.exe               Windows-Installer (wird still ausgefuehrt: /S)
//   TokenOrb-Mac-<ver>.zip                 Mac-App
//   TokenOrb-ESP32-C3-SuperMini-update.bin Firmware (App-Image fuer das Update per USB)
//   ...                                    (weitere Boards)
// Jede Datei wird vor der Verwendung gegen die SHA-256 aus manifest.json geprueft.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GitHub-Repo "besitzer/name" (oeffentlich). Leer = Update-Quelle nicht eingerichtet;
// laesst sich in config.json mit "updateRepo" ueberschreiben.
const defaultUpdateRepo = "web-smith-agency/tokenorb"

const maxDownload = 64 << 20

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type ghRelease struct {
	Tag    string    `json:"tag_name"`
	Body   string    `json:"body"`
	Draft  bool      `json:"draft"`
	Pre    bool      `json:"prerelease"`
	Assets []ghAsset `json:"assets"`
}

type manifestFile struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type releaseManifest struct {
	App      string                  `json:"app"`
	Firmware string                  `json:"firmware"`
	Notes    string                  `json:"notes"`
	Files    map[string]manifestFile `json:"files"`
}

type releaseInfo struct {
	Tag      string
	Manifest releaseManifest
	Assets   map[string]string // Name -> Download-Adresse
}

// Dateinamen der Firmware je Board (Board = 3. Wort der Handshake-Antwort)
var firmwareAssets = map[string]string{
	"C3":    "TokenOrb-ESP32-C3-SuperMini-update.bin",
	"S3":    "TokenOrb-ESP32-S3-N16R8-update.bin",
	"WROOM": "TokenOrb-ESP32-WROOM-update.bin",
}

func appAssetName(goos, version string) string {
	if goos == "darwin" {
		return "TokenOrb-Mac-" + version + ".zip"
	}
	return "TokenOrb-Setup-" + version + ".exe"
}

// versionLess vergleicht "1.2.10" < "1.3" numerisch (fuehrendes "v" erlaubt).
func versionLess(a, b string) bool {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

func versionParts(v string) [3]int {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	for i, p := range strings.SplitN(v, ".", 3) {
		n, _ := strconv.Atoi(p)
		out[i] = n
	}
	return out
}

// ---------------------------------------------------------------------------

type updater struct {
	mu      sync.Mutex
	client  *http.Client
	apiBase string // https://api.github.com (in Tests ein lokaler Server)
	goos    string

	checked time.Time
	rel     *releaseInfo
	err     string

	busy     string // "", "app", "firmware"
	progress int    // 0..100 beim Herunterladen
	msg      string // letzte Meldung fuer die Oberflaeche
	msgErr   bool
}

func newUpdater(goos string) *updater {
	u := &updater{client: &http.Client{Timeout: 60 * time.Second}, apiBase: "https://api.github.com", goos: goos}
	if v := os.Getenv("TOKENORB_UPDATE_API"); v != "" { // nur zum Testen
		u.apiBase = strings.TrimRight(v, "/")
	}
	return u
}

func updateRepo() string {
	if r := strings.TrimSpace(getConfig().UpdateRepo); r != "" {
		return r
	}
	return defaultUpdateRepo
}

func (u *updater) get(ctx context.Context, url string, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", appName+"/"+appVersion)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusNotFound:
			return nil, errors.New("kein Release gefunden (Repo öffentlich? Release veröffentlicht?)")
		case http.StatusForbidden, http.StatusTooManyRequests:
			return nil, errors.New("GitHub bremst gerade (zu viele Anfragen) – später nochmal")
		}
		return nil, fmt.Errorf("GitHub antwortet mit %s", resp.Status)
	}
	return resp, nil
}

// Check holt das neueste Release und dessen manifest.json.
func (u *updater) Check(ctx context.Context, repo string) error {
	rel, err := u.fetch(ctx, repo)
	u.mu.Lock()
	defer u.mu.Unlock()
	u.checked = time.Now()
	if err != nil {
		u.err = err.Error()
		return err
	}
	u.rel, u.err = rel, ""
	return nil
}

func (u *updater) fetch(ctx context.Context, repo string) (*releaseInfo, error) {
	if repo == "" {
		return nil, errors.New("Update-Quelle ist noch nicht eingerichtet")
	}
	if strings.Count(repo, "/") != 1 || strings.ContainsAny(repo, " ?#") {
		return nil, fmt.Errorf("Update-Quelle %q ist kein GitHub-Repo (besitzer/name)", repo)
	}
	resp, err := u.get(ctx, u.apiBase+"/repos/"+repo+"/releases/latest", "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	var gr ghRelease
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&gr)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("Antwort von GitHub unlesbar: %w", err)
	}
	ri := &releaseInfo{Tag: gr.Tag, Assets: map[string]string{}}
	for _, a := range gr.Assets {
		ri.Assets[a.Name] = a.URL
	}
	murl, ok := ri.Assets["manifest.json"]
	if !ok {
		return nil, errors.New("Release " + gr.Tag + " hat keine manifest.json")
	}
	mresp, err := u.get(ctx, murl, "")
	if err != nil {
		return nil, err
	}
	err = json.NewDecoder(io.LimitReader(mresp.Body, 1<<20)).Decode(&ri.Manifest)
	mresp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("manifest.json unlesbar: %w", err)
	}
	if ri.Manifest.App == "" {
		ri.Manifest.App = strings.TrimPrefix(gr.Tag, "v")
	}
	if ri.Manifest.Notes == "" {
		ri.Manifest.Notes = gr.Body
	}
	return ri, nil
}

// Download laedt eine Datei aus dem Release und prueft die SHA-256 aus manifest.json.
func (u *updater) Download(ctx context.Context, name string, progress func(pct int)) ([]byte, error) {
	u.mu.Lock()
	rel := u.rel
	u.mu.Unlock()
	if rel == nil {
		return nil, errors.New("erst nach Updates suchen")
	}
	url, ok := rel.Assets[name]
	if !ok {
		return nil, fmt.Errorf("%s fehlt im Release %s", name, rel.Tag)
	}
	want, ok := rel.Manifest.Files[name]
	if !ok || len(want.SHA256) != 64 {
		return nil, fmt.Errorf("keine Prüfsumme für %s in manifest.json", name)
	}
	resp, err := u.get(ctx, url, "application/octet-stream")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	total := want.Size
	if total <= 0 {
		total = resp.ContentLength
	}
	var buf []byte
	chunk := make([]byte, 64<<10)
	for {
		n, rerr := resp.Body.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if len(buf) > maxDownload {
			return nil, errors.New("Datei ist zu groß")
		}
		if progress != nil && total > 0 {
			progress(int(int64(len(buf)) * 100 / total))
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, fmt.Errorf("Download abgebrochen: %w", rerr)
		}
	}
	sum := sha256.Sum256(buf)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), want.SHA256) {
		return nil, fmt.Errorf("Prüfsumme von %s stimmt nicht – Download verworfen", name)
	}
	return buf, nil
}

// --- Zustand fuer die Oberflaeche ---------------------------------------------

type updateStatus struct {
	Repo        string `json:"repo"`
	Checked     int64  `json:"checked"`
	Error       string `json:"error"`
	Latest      string `json:"latest"`
	AppUpdate   bool   `json:"appUpdate"`
	Notes       string `json:"notes"`
	FwLatest    string `json:"fwLatest"`
	FwDevice    string `json:"fwDevice"`
	Board       string `json:"board"`
	FwUpdate    bool   `json:"fwUpdate"`
	FwCapable   bool   `json:"fwCapable"`
	FwUSB       bool   `json:"fwUSB"`
	FwAvailable bool   `json:"fwAvailable"` // Datei fuer dieses Board im Release
	Busy        string `json:"busy"`
	Progress    int    `json:"progress"`
	Msg         string `json:"msg"`
	MsgErr      bool   `json:"msgErr"`
}

func (u *updater) Status(devID string, usb bool) updateStatus {
	u.mu.Lock()
	defer u.mu.Unlock()
	st := updateStatus{Repo: updateRepo(), Error: u.err, Busy: u.busy, Progress: u.progress, Msg: u.msg, MsgErr: u.msgErr}
	if !u.checked.IsZero() {
		st.Checked = u.checked.Unix()
	}
	st.FwDevice, st.Board = parseDeviceID(devID)
	st.FwCapable = otaCapable(st.FwDevice)
	st.FwUSB = usb
	if u.rel != nil {
		m := u.rel.Manifest
		st.Latest, st.Notes, st.FwLatest = m.App, m.Notes, m.Firmware
		st.AppUpdate = versionLess(appVersion, m.App)
		if name, ok := firmwareAssets[st.Board]; ok {
			_, st.FwAvailable = u.rel.Assets[name]
		}
		st.FwUpdate = st.FwDevice != "" && m.Firmware != "" && versionLess(st.FwDevice, m.Firmware)
	}
	return st
}

func (u *updater) setBusy(what string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.busy != "" {
		return false
	}
	u.busy, u.progress, u.msg, u.msgErr = what, 0, "", false
	return true
}

func (u *updater) setProgress(p int) {
	u.mu.Lock()
	u.progress = p
	u.mu.Unlock()
}

func (u *updater) finish(msg string, isErr bool) {
	u.mu.Lock()
	u.busy, u.msg, u.msgErr = "", msg, isErr
	u.mu.Unlock()
	if isErr {
		logf("Update: %s", msg)
	}
}

func (u *updater) note(msg string) {
	u.mu.Lock()
	u.msg, u.msgErr = msg, false
	u.mu.Unlock()
}

// latestApp liefert die neuere App-Version ("" = keine).
func (u *updater) latestApp() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.rel != nil && versionLess(appVersion, u.rel.Manifest.App) {
		return u.rel.Manifest.App
	}
	return ""
}

// StartAppUpdate laedt den passenden Installer und uebergibt an applyAppUpdate.
func (u *updater) StartAppUpdate() error {
	ver := u.latestApp()
	if ver == "" {
		return errors.New("keine neuere Version vorhanden")
	}
	if !u.setBusy("app") {
		return errors.New("es läuft bereits ein Update")
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		name := appAssetName(u.goos, ver)
		data, err := u.Download(ctx, name, u.setProgress)
		if err != nil {
			u.finish(err.Error(), true)
			return
		}
		u.note("Installiere " + ver + " … TokenOrb startet gleich neu.")
		if err := applyAppUpdate(name, data); err != nil {
			u.finish("Installation fehlgeschlagen: "+err.Error(), true)
		}
	}()
	return nil
}

// runUpdateChecker prueft beim Start und dann alle 6 Stunden.
func runUpdateChecker(u *updater, onNew func(version string)) {
	time.Sleep(15 * time.Second)
	notified := ""
	for {
		if !getConfig().NoUpdateCheck {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			_ = u.Check(ctx, updateRepo())
			cancel()
			if v := u.latestApp(); v != "" && v != notified && onNew != nil {
				notified = v
				onNew(v)
			}
		}
		time.Sleep(6 * time.Hour)
	}
}
