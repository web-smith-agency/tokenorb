//go:build !windows && !darwin

package main

import "errors"

// Nur damit updater.go auch unter Linux (Tests) baut.
func applyAppUpdate(name string, data []byte) error {
	return errors.New("Updates gibt es nur unter Windows und macOS")
}
