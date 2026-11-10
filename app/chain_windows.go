//go:build windows

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

const createNoWindow = 0x08000000

// findBash sucht Git Bash – die nutzt Claude Code unter Windows auch.
func findBash() string {
	if p := os.Getenv("CLAUDE_CODE_GIT_BASH_PATH"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	candidates := []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Git", "bin", "bash.exe"),
		filepath.Join(os.Getenv("ProgramW6432"), "Git", "bin", "bash.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Git", "bin", "bash.exe"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	if p, err := exec.LookPath("bash.exe"); err == nil {
		return p
	}
	return ""
}

// runChain fuehrt die vorherige Statuszeile des Nutzers mit denselben Daten aus.
func runChain(command string, stdin []byte, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var cmd *exec.Cmd
	if bash := findBash(); bash != "" {
		cmd = exec.CommandContext(ctx, bash, "-c", command)
	} else {
		cmd = exec.CommandContext(ctx, "cmd.exe", "/C", command)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	cmd.Stdin = bytes.NewReader(stdin)
	out, err := cmd.Output()
	return string(out), err
}

// hideWindow verhindert, dass Konsolenprogramme (z.B. claude) ein Fenster aufpoppen lassen.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
