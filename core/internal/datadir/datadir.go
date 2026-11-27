// Package datadir resolves and prepares the Tolarix data directory.
//
// Resolution order: explicit flag value, then TOLARIX_DATA_DIR, then the OS
// default. The desktop app and the CLI both use the OS default, so they share
// one database and the same flows.
package datadir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// EnvVar is the environment variable that overrides the data directory.
const EnvVar = "TOLARIX_DATA_DIR"

// DatabaseFile is the SQLite file name inside the data directory.
const DatabaseFile = "tolarix.db"

// Resolve returns the absolute data directory path. flagValue wins when non-empty.
func Resolve(flagValue string) (string, error) {
	dir := flagValue
	if dir == "" {
		dir = os.Getenv(EnvVar)
	}
	if dir == "" {
		def, err := Default()
		if err != nil {
			return "", err
		}
		dir = def
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve data directory %q: %w", dir, err)
	}
	return abs, nil
}

// Default returns the per-OS application data folder:
//
//	Windows: %AppData%\Tolarix
//	macOS:   ~/Library/Application Support/Tolarix
//	Linux:   $XDG_CONFIG_HOME/tolarix (usually ~/.config/tolarix)
func Default() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot find the user config directory; set %s or pass --data-dir: %w", EnvVar, err)
	}
	name := "Tolarix"
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		name = "tolarix"
	}
	return filepath.Join(base, name), nil
}

// Ensure creates the directory (0700) if needed and checks it is writable.
func Ensure(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create data directory %q: %w", dir, err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("stat data directory %q: %w", dir, err)
	}
	if !fi.IsDir() {
		return errors.New("data directory path exists but is not a directory: " + dir)
	}
	probe, err := os.CreateTemp(dir, ".write-test-*")
	if err != nil {
		return fmt.Errorf("data directory %q is not writable: %w", dir, err)
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return nil
}

// DatabasePath returns the SQLite file path inside dir.
func DatabasePath(dir string) string {
	return filepath.Join(dir, DatabaseFile)
}
