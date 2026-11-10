package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeGitHub liefert /repos/x/y/releases/latest und die Dateien.
func fakeGitHub(t *testing.T, files map[string][]byte, man releaseManifest, tag string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/repos/web/orb/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("kein User-Agent")
		}
		rel := ghRelease{Tag: tag, Body: "Notizen"}
		for name := range files {
			rel.Assets = append(rel.Assets, ghAsset{Name: name, URL: srv.URL + "/dl/" + name})
		}
		rel.Assets = append(rel.Assets, ghAsset{Name: "manifest.json", URL: srv.URL + "/dl/manifest.json"})
		_ = json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/dl/")
		if name == "manifest.json" {
			_ = json.NewEncoder(w).Encode(man)
			return
		}
		b, ok := files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	})
	srv = httptest.NewServer(mux)
	return srv
}

func shaHex(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func TestUpdaterCheckAndDownload(t *testing.T) {
	t.Setenv("TOKENORB_HOME", t.TempDir())
	reloadConfig()
	setup := []byte("MZ...installer")
	fw := testImage(9000)
	man := releaseManifest{App: "9.9.0", Firmware: "9.1.0", Files: map[string]manifestFile{
		"TokenOrb-Setup-9.9.0.exe":               {SHA256: shaHex(setup), Size: int64(len(setup))},
		"TokenOrb-ESP32-C3-SuperMini-update.bin": {SHA256: shaHex(fw), Size: int64(len(fw))},
		"TokenOrb-ESP32-WROOM-update.bin":        {SHA256: strings.Repeat("a", 64)}, // falsche Summe
	}}
	files := map[string][]byte{
		"TokenOrb-Setup-9.9.0.exe":               setup,
		"TokenOrb-ESP32-C3-SuperMini-update.bin": fw,
		"TokenOrb-ESP32-WROOM-update.bin":        []byte("kaputt"),
	}
	srv := fakeGitHub(t, files, man, "v9.9.0")
	defer srv.Close()

	u := newUpdater("windows")
	u.apiBase = srv.URL
	if err := u.Check(context.Background(), "web/orb"); err != nil {
		t.Fatal(err)
	}
	st := u.Status("ORB 1.2.0 C3", true)
	if !st.AppUpdate || st.Latest != "9.9.0" || !st.FwUpdate || !st.FwAvailable || !st.FwCapable || st.Board != "C3" {
		t.Fatalf("Status falsch: %+v", st)
	}
	if st2 := u.Status("ORB 1.1.0 S3", true); st2.FwCapable || st2.FwAvailable {
		t.Fatalf("S3 ohne Datei / alte FW falsch: %+v", st2)
	}
	if u.latestApp() != "9.9.0" {
		t.Fatal("latestApp")
	}
	var last int
	b, err := u.Download(context.Background(), appAssetName("windows", "9.9.0"), func(p int) { last = p })
	if err != nil || string(b) != string(setup) || last != 100 {
		t.Fatalf("Download: %v %q %d", err, b, last)
	}
	if _, err := u.Download(context.Background(), "TokenOrb-ESP32-WROOM-update.bin", nil); err == nil || !strings.Contains(err.Error(), "Prüfsumme") {
		t.Fatalf("falsche Pruefsumme nicht erkannt: %v", err)
	}
	if _, err := u.Download(context.Background(), "gibtsnicht.bin", nil); err == nil {
		t.Fatal("fehlende Datei nicht erkannt")
	}
}

func TestUpdaterErrors(t *testing.T) {
	t.Setenv("TOKENORB_HOME", t.TempDir())
	reloadConfig()
	u := newUpdater("darwin")
	if err := u.Check(context.Background(), ""); err == nil {
		t.Fatal("leere Quelle")
	}
	if err := u.Check(context.Background(), "https://github.com/a/b"); err == nil {
		t.Fatal("URL statt Repo")
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	u.apiBase = srv.URL
	err := u.Check(context.Background(), "web/orb")
	if err == nil || !strings.Contains(err.Error(), "kein Release") {
		t.Fatalf("404: %v", err)
	}
	if st := u.Status("", false); st.Error == "" || st.AppUpdate {
		t.Fatalf("Status nach Fehler: %+v", st)
	}
	// gleiche Version -> kein Update
	srv2 := fakeGitHub(t, map[string][]byte{}, releaseManifest{App: appVersion}, "v"+appVersion)
	defer srv2.Close()
	u.apiBase = srv2.URL
	if err := u.Check(context.Background(), "web/orb"); err != nil {
		t.Fatal(err)
	}
	if u.Status("", false).AppUpdate || u.latestApp() != "" {
		t.Fatal("gleiche Version als Update erkannt")
	}
	if appAssetName("darwin", "1.3.0") != "TokenOrb-Mac-1.3.0.zip" {
		t.Fatal("Mac-Dateiname")
	}
}
