//go:build !windows

package main

import (
	"bytes"
	"context"
	"os/exec"
	"time"
)

func runChain(command string, stdin []byte, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Stdin = bytes.NewReader(stdin)
	out, err := cmd.Output()
	return string(out), err
}

func hideWindow(cmd *exec.Cmd) {}
