package main

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var logMu sync.Mutex

// logf schreibt in tokenorb.log (max. ca. 512 KB, wird dann neu begonnen).
func logf(format string, args ...any) {
	logMu.Lock()
	defer logMu.Unlock()
	p := logPath()
	if fi, err := os.Stat(p); err == nil && fi.Size() > 512*1024 {
		_ = os.Remove(p)
	}
	_ = os.MkdirAll(appDir(), 0o755)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s  %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
}
