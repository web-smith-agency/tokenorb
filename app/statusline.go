package main

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// runStatusLine wird von Claude Code bei jeder Aktualisierung der Statuszeile
// aufgerufen (Modus "statusline"). Muss schnell sein und darf nie haengen.
func runStatusLine(stdin io.Reader, stdout io.Writer) {
	data, _ := io.ReadAll(io.LimitReader(stdin, 1<<20))
	now := time.Now()

	st, ok := ParseStatusLineInput(data, now)
	if ok {
		// nur schreiben, wenn wirklich Limit-Daten drin sind – sonst den letzten
		// bekannten Stand behalten (z.B. direkt nach dem Start einer Sitzung)
		_ = writeState(statePath(), st)
	}

	cfg := loadConfig()
	if chain := strings.TrimSpace(cfg.ChainStatusLine); chain != "" {
		if out, err := runChain(chain, data, 4*time.Second); err == nil {
			fmt.Fprint(stdout, out)
			return
		}
	}

	// eigene, schlichte Statuszeile
	if !ok {
		if prev, err := readState(statePath()); err == nil && (prev.FiveHour != nil || prev.SevenDay != nil) {
			st = prev
		} else {
			fmt.Fprintln(stdout, "TokenOrb: warte auf Limit-Daten")
			return
		}
	}
	line := SummaryText(st, now)
	if st.Model != "" {
		line = st.Model + "  |  " + line
	}
	fmt.Fprintln(stdout, line)
}
