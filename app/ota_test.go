package main

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePuck bildet die Firmware-Seite nach (firmware/src/comms.cpp: otaStart/otaLoop).
type fakePuck struct {
	mu       sync.Mutex
	lines    chan string
	done     chan struct{}
	active   bool
	size     int
	md5      string
	got      []byte
	acked    int
	finished bool
	// Fehlerbilder
	failAt     int  // nach so vielen Bytes "F ERR schreiben" (0 = nie)
	silent     bool // antwortet nie
	dropAfter  int  // Verbindung nach so vielen Bytes trennen (0 = nie)
	writeChunk int  // Hilfsgroesse: wie die Firmware in Stuecken liest
}

func newFakePuck() *fakePuck {
	return &fakePuck{lines: make(chan string, 64), done: make(chan struct{}), writeChunk: 512}
}

func (f *fakePuck) Lines() <-chan string  { return f.lines }
func (f *fakePuck) Done() <-chan struct{} { return f.done }
func (f *fakePuck) say(s string)          { f.lines <- s }

func (f *fakePuck) Send(line string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.silent {
		return nil
	}
	p := strings.Fields(line)
	if len(p) == 3 && p[0] == "F" && !f.active {
		n, _ := strconv.Atoi(p[1])
		if n <= 0 {
			f.say("F ERR groesse")
			return nil
		}
		f.active, f.size, f.md5, f.got, f.acked = true, n, p[2], nil, 0
		f.say("F OK")
	}
	return nil
}

func (f *fakePuck) Write(b []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.active {
		return len(b), nil // Muell im Zeilenmodus
	}
	for off := 0; off < len(b); off += f.writeChunk {
		end := off + f.writeChunk
		if end > len(b) {
			end = len(b)
		}
		f.got = append(f.got, b[off:end]...)
		if f.dropAfter > 0 && len(f.got) >= f.dropAfter {
			close(f.done)
			return end, nil
		}
		if f.failAt > 0 && len(f.got) >= f.failAt {
			f.active = false
			f.say("F ERR schreiben")
			return len(b), nil
		}
		if len(f.got) == f.size {
			sum := md5.Sum(f.got)
			if fmt.Sprintf("%x", sum) != f.md5 {
				f.active = false
				f.say("F ERR md5")
				return len(b), nil
			}
			f.say("F DONE")
			f.finished = true
			f.active = false
			return len(b), nil
		}
		if len(f.got)-f.acked >= otaBlock {
			f.acked = len(f.got) - len(f.got)%otaBlock
			f.say(fmt.Sprintf("F %d", f.acked))
		}
	}
	return len(b), nil
}

func testImage(n int) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(int64(n))).Read(b)
	b[0] = 0xE9
	return b
}

func TestOtaFlashSizes(t *testing.T) {
	for _, n := range []int{1024, otaBlock, otaBlock + 1, 3*otaBlock - 7, 5 * otaBlock, 821136} {
		f := newFakePuck()
		img := testImage(n)
		var lastDone int
		err := otaFlash(f, img, func(d, total int) { lastDone = d })
		if err != nil {
			t.Fatalf("%d Byte: %v", n, err)
		}
		if !f.finished || !bytes.Equal(f.got, img) {
			t.Fatalf("%d Byte: Daten kamen nicht vollstaendig an", n)
		}
		if lastDone != n {
			t.Fatalf("%d Byte: Fortschritt endet bei %d", n, lastDone)
		}
	}
}

func TestOtaFlashErrors(t *testing.T) {
	otaStartTimeout, otaBlockTimeout = 300*time.Millisecond, 300*time.Millisecond
	defer func() { otaStartTimeout, otaBlockTimeout = 15*time.Second, 20*time.Second }()

	f := newFakePuck()
	f.failAt = 3 * otaBlock
	if err := otaFlash(f, testImage(10*otaBlock), nil); err == nil || !strings.Contains(err.Error(), "schreiben") {
		t.Fatalf("Schreibfehler nicht gemeldet: %v", err)
	}

	f = newFakePuck()
	f.silent = true
	if err := otaFlash(f, testImage(2*otaBlock), nil); err == nil || !strings.Contains(err.Error(), "antwortet nicht") {
		t.Fatalf("stummer Puck nicht erkannt: %v", err)
	}

	f = newFakePuck()
	f.dropAfter = otaBlock + 100
	if err := otaFlash(f, testImage(4*otaBlock), nil); err != errOtaLost {
		t.Fatalf("Abbruch nicht erkannt: %v", err)
	}

	// keine Firmware (z.B. falsche Datei gewaehlt) -> sofort ablehnen
	if err := otaFlash(newFakePuck(), bytes.Repeat([]byte("x"), 5000), nil); err == nil {
		t.Fatal("keine Firmware-Datei nicht abgelehnt")
	}

	// md5 falsch: Puck vergleicht und meldet
	f = newFakePuck()
	img := testImage(2 * otaBlock)
	f2 := &md5Lie{fakePuck: f}
	if err := otaFlash(f2, img, nil); err == nil || !strings.Contains(err.Error(), "md5") {
		t.Fatalf("md5-Fehler nicht gemeldet: %v", err)
	}
}

// md5Lie verfaelscht die angekuendigte Pruefsumme.
type md5Lie struct{ *fakePuck }

func (m *md5Lie) Send(line string) error {
	if strings.HasPrefix(line, "F ") {
		p := strings.Fields(line)
		p[2] = strings.Repeat("0", 32)
		line = strings.Join(p, " ")
	}
	return m.fakePuck.Send(line)
}

func TestDeviceIDAndVersions(t *testing.T) {
	v, b := parseDeviceID("ORB 1.2.0 C3")
	if v != "1.2.0" || b != "C3" {
		t.Fatal(v, b)
	}
	if otaCapable("1.1.0") || !otaCapable("1.2.0") || !otaCapable("1.10.0") || otaCapable("") {
		t.Fatal("otaCapable")
	}
	cases := []struct {
		a, b string
		less bool
	}{{"1.2.0", "1.3.0", true}, {"1.2.9", "1.2.10", true}, {"v1.3.0", "1.3.0", false}, {"1.3", "1.3.0", false}, {"2.0.0", "1.9.9", false}, {"1.3.0-beta", "1.3.1", true}}
	for _, c := range cases {
		if versionLess(c.a, c.b) != c.less {
			t.Errorf("versionLess(%q,%q) != %v", c.a, c.b, c.less)
		}
	}
}
