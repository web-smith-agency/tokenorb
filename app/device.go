//go:build windows || darwin

package main

// Verbindung zum Puck (USB seriell, unter Windows zusaetzlich Bluetooth) –
// gemeinsam fuer Windows und Mac. Plattformspezifisch sind nur die Port-Suche
// (serialCandidates) und Bluetooth (connectBLE), siehe device_windows.go / device_darwin.go.

import (
	"crypto/md5"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
)

// link ist eine Verbindung zum Geraet – USB (seriell) oder Bluetooth.
type link interface {
	Name() string
	ID() string // Antwort auf "?", z.B. "ORB 1.2.0 C3"
	Send(line string) error
	Lines() <-chan string
	Done() <-chan struct{}
	Close()
}

// lineSplitter sammelt Bytes und gibt vollstaendige Zeilen weiter.
type lineSplitter struct {
	acc []byte
	out chan string
}

func (s *lineSplitter) feed(b []byte) {
	for _, c := range b {
		switch c {
		case '\r':
		case '\n':
			if len(s.acc) > 0 {
				select {
				case s.out <- string(s.acc):
				default: // Leser zu langsam -> verwerfen statt blockieren
				}
			}
			s.acc = s.acc[:0]
		default:
			if len(s.acc) < 256 {
				s.acc = append(s.acc, c)
			}
		}
	}
}

// ===========================================================================
// USB / seriell
// ===========================================================================

type serialLink struct {
	port  serial.Port
	name  string
	id    string
	split lineSplitter
	done  chan struct{}
	once  sync.Once
	wmu   sync.Mutex
}

func openSerialLink(name string) (*serialLink, error) {
	mode := &serial.Mode{
		BaudRate: 115200,
		// DTR/RTS aus: sonst setzen manche Boards beim Oeffnen zurueck
		InitialStatusBits: &serial.ModemOutputBits{DTR: false, RTS: false},
	}
	p, err := serial.Open(name, mode)
	if err != nil {
		return nil, err
	}
	_ = p.SetReadTimeout(200 * time.Millisecond)
	l := &serialLink{port: p, name: name, done: make(chan struct{})}
	l.split.out = make(chan string, 32)
	go l.reader()
	return l, nil
}

func (l *serialLink) reader() {
	buf := make([]byte, 256)
	for {
		n, err := l.port.Read(buf)
		if err != nil {
			l.Close()
			return
		}
		select {
		case <-l.done:
			return
		default:
		}
		if n > 0 {
			l.split.feed(buf[:n])
		}
	}
}

func (l *serialLink) Name() string          { return "USB (" + l.name + ")" }
func (l *serialLink) ID() string            { return l.id }
func (l *serialLink) Lines() <-chan string  { return l.split.out }
func (l *serialLink) Done() <-chan struct{} { return l.done }
func (l *serialLink) Send(line string) error {
	_, err := l.Write([]byte(line + "\n"))
	return err
}

// Write schreibt rohe Bytes vollstaendig (fuer das Firmware-Update).
func (l *serialLink) Write(b []byte) (int, error) {
	l.wmu.Lock()
	defer l.wmu.Unlock()
	total := 0
	for total < len(b) {
		n, err := l.port.Write(b[total:])
		total += n
		if err != nil {
			l.Close()
			return total, err
		}
		if n == 0 {
			l.Close()
			return total, errors.New("Port schreibt nicht")
		}
	}
	return total, nil
}

func (l *serialLink) Close() {
	l.once.Do(func() {
		close(l.done)
		_ = l.port.Close()
	})
}

// handshake: "?" senden, auf "ORB ..." warten
func handshake(l link, timeout time.Duration) (string, bool) {
	if err := l.Send("?"); err != nil {
		return "", false
	}
	deadline := time.After(timeout)
	for {
		select {
		case line := <-l.Lines():
			if strings.HasPrefix(line, "ORB ") {
				return line, true
			}
		case <-l.Done():
			return "", false
		case <-deadline:
			return "", false
		}
	}
}

func findSerial(lastPort string) link {
	for _, name := range serialCandidates(lastPort) {
		l, err := openSerialLink(name)
		if err != nil {
			continue // z.B. von der Arduino-IDE oder dem Web-Flasher belegt
		}
		// ESP32 braucht nach dem Oeffnen einen Moment
		time.Sleep(300 * time.Millisecond)
		if id, ok := handshake(l, 1500*time.Millisecond); ok {
			l.id = id
			logf("Geraet an %s gefunden: %s", name, id)
			return l
		}
		l.Close()
	}
	return nil
}

// ===========================================================================
// Verbindungsverwaltung
// ===========================================================================

type flashJob struct {
	img  []byte
	done chan error
}

type deviceManager struct {
	mu       sync.Mutex
	status   string
	devID    string // Handshake des verbundenen Geraets ("" = keins)
	viaUSB   bool
	paused   bool // Port freigegeben (z.B. fuer den Web-Flasher)
	cmds     chan string
	flashCh  chan *flashJob
	flashPct int
	onKey    func(n int)
	onChange func()
}

func newDeviceManager(onKey func(int), onChange func()) *deviceManager {
	return &deviceManager{status: "Geraet: suche ...", cmds: make(chan string, 16),
		flashCh: make(chan *flashJob), flashPct: -1, onKey: onKey, onChange: onChange}
}

func (m *deviceManager) Status() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// Device liefert Handshake-Zeile und ob per USB verbunden ("" = nicht verbunden).
func (m *deviceManager) Device() (id string, usb bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.devID, m.viaUSB
}

func (m *deviceManager) FlashProgress() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.flashPct
}

func (m *deviceManager) setStatus(s string) {
	m.mu.Lock()
	changed := m.status != s
	m.status = s
	m.mu.Unlock()
	if changed && m.onChange != nil {
		m.onChange()
	}
}

func (m *deviceManager) setDevice(id string, usb bool) {
	m.mu.Lock()
	m.devID, m.viaUSB = id, usb
	m.mu.Unlock()
}

// Pause gibt den USB-Port frei (z.B. fuer den Web-Flasher), Resume sucht wieder.
func (m *deviceManager) SetPaused(p bool) {
	m.mu.Lock()
	m.paused = p
	m.mu.Unlock()
	m.Command("") // laufende Verbindung aufwecken
}

func (m *deviceManager) Paused() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.paused
}

// Command schickt einen Befehl ans Geraet (wird verworfen, wenn keins verbunden ist).
func (m *deviceManager) Command(line string) {
	select {
	case m.cmds <- line:
	default:
	}
}

// Flash spielt eine Firmware (App-Image, nicht die zusammengefasste .bin) per USB auf.
// Blockiert bis zum Ende; progress laeuft ueber FlashProgress().
func (m *deviceManager) Flash(img []byte) error {
	id, usb := m.Device()
	if id == "" {
		return errors.New("kein Puck verbunden")
	}
	if !usb {
		return errors.New("Firmware-Update geht nur per USB-Kabel")
	}
	if ver, _ := parseDeviceID(id); !otaCapable(ver) {
		return fmt.Errorf("die Firmware %s auf dem Puck kann sich noch nicht selbst aktualisieren – einmalig per Web-Flasher aufspielen", ver)
	}
	job := &flashJob{img: img, done: make(chan error, 1)}
	select {
	case m.flashCh <- job:
	case <-time.After(5 * time.Second):
		return errors.New("Puck antwortet nicht")
	}
	return <-job.done
}

func (m *deviceManager) run() {
	bleUnavailable := false
	for {
		if m.Paused() {
			m.setDevice("", false)
			m.setStatus("Geraet: pausiert (USB frei)")
			m.drain()
			time.Sleep(time.Second)
			continue
		}
		cfg := getConfig()
		var l link
		usb := false
		if s := findSerial(cfg.LastPort); s != nil {
			l, usb = s, true
			if sl, ok := s.(*serialLink); ok && sl.name != cfg.LastPort {
				updateConfig(func(c *Config) { c.LastPort = sl.name })
			}
		} else if cfg.UseBluetooth && !bleUnavailable {
			m.setStatus("Geraet: suche (USB + Bluetooth) ...")
			b, err := connectBLE(6 * time.Second)
			if errors.Is(err, errNoAdapter) {
				bleUnavailable = true
				logf("Bluetooth nicht verfuegbar – nur USB")
			}
			if err == nil {
				l = b
			}
		}
		if l == nil {
			m.setDevice("", false)
			m.setStatus("Geraet: nicht gefunden")
			m.drain()
			time.Sleep(4 * time.Second)
			continue
		}
		m.setDevice(l.ID(), usb)
		m.serve(l)
		l.Close()
		m.setDevice("", false)
		logf("Verbindung getrennt")
		m.setStatus("Geraet: getrennt")
		time.Sleep(1 * time.Second)
	}
}

// drain verwirft alte Befehle und beantwortet Update-Auftraege mit Fehler.
func (m *deviceManager) drain() {
	for {
		select {
		case <-m.cmds:
		case job := <-m.flashCh:
			job.done <- errors.New("kein Puck verbunden")
		default:
			return
		}
	}
}

func (m *deviceManager) serve(l link) {
	m.setStatus("Geraet: " + l.Name())
	cfg := getConfig()
	if l.Send(fmt.Sprintf("B %d", cfg.Brightness)) != nil {
		return
	}
	var lastMod time.Time
	var lastSend time.Time
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	first := true
	for {
		select {
		case <-l.Done():
			return
		case line := <-l.Lines():
			if strings.HasPrefix(line, "K ") {
				if n, err := strconv.Atoi(strings.TrimSpace(line[2:])); err == nil && m.onKey != nil {
					go m.onKey(n)
				}
			}
		case c := <-m.cmds:
			if m.Paused() {
				return
			}
			if c == "" {
				continue
			}
			if l.Send(c) != nil {
				return
			}
		case job := <-m.flashCh:
			sl, ok := l.(*serialLink)
			if !ok {
				job.done <- errors.New("Firmware-Update geht nur per USB-Kabel")
				continue
			}
			m.setStatus("Geraet: Firmware-Update ...")
			err := otaFlash(sl, job.img, func(done, total int) {
				m.mu.Lock()
				m.flashPct = done * 100 / total
				m.mu.Unlock()
			})
			m.mu.Lock()
			m.flashPct = -1
			m.mu.Unlock()
			if err != nil {
				logf("Firmware-Update: %v", err)
			} else {
				logf("Firmware-Update erfolgreich (%d Byte, md5 %x)", len(job.img), md5.Sum(job.img))
			}
			job.done <- err
			return // Geraet startet neu bzw. Verbindung neu aufbauen
		case <-tick.C:
			fi, err := os.Stat(statePath())
			changed := err == nil && !fi.ModTime().Equal(lastMod)
			if first || changed || time.Since(lastSend) > 15*time.Second {
				first = false
				line := "P"
				if st, rerr := readState(statePath()); rerr == nil {
					line = DeviceUpdateLine(st, time.Now())
					lastMod = fi.ModTime()
				}
				if l.Send(line) != nil {
					return
				}
				lastSend = time.Now()
			}
		}
	}
}
