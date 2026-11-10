//go:build !windows && !darwin

package main

import (
	"fmt"
	"os"
)

// Die Tray-App gibt es nur fuer Windows. Die Statuszeile funktioniert aber
// ueberall (praktisch zum Testen unter Linux/macOS).
func main() {
	if len(os.Args) > 1 && os.Args[1] == "statusline" {
		runStatusLine(os.Stdin, os.Stdout)
		return
	}
	fmt.Println(appName, appVersion, "- Tray-App nur unter Windows verfuegbar.")
}
