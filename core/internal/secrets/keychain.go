package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

// EnvKeyStore chooses where a new key goes: "keychain" (the OS keychain:
// macOS Keychain, Windows Credential Manager, the Secret Service on Linux)
// or "file" (the default). An existing key is always found where it is.
const EnvKeyStore = "TOLARIX_SECRET_KEY_STORE"

// KeychainMarkerName is the file in the data directory that says the key is
// in the OS keychain. Every process using the data directory (desktop app,
// CLI) reads it to find the key; only processes that see it touch the
// keychain, so Docker and CI never do.
const KeychainMarkerName = "secret.key.keychain"

// KeychainService is the service name of the keychain entry.
const KeychainService = "Tolarix"

// Keychain is the OS keychain; tests replace it.
type Keychain interface {
	Get(service, account string) (string, error)
	Set(service, account, secret string) error
	Delete(service, account string) error
}

type osKeychain struct{}

func (osKeychain) Get(s, a string) (string, error) { return keyring.Get(s, a) }
func (osKeychain) Set(s, a, v string) error        { return keyring.Set(s, a, v) }
func (osKeychain) Delete(s, a string) error        { return keyring.Delete(s, a) }

// OSKeychain is the keychain LoadKey uses.
var OSKeychain Keychain = osKeychain{}

// ErrKeychainNotFound is what a Keychain returns for a missing entry.
var ErrKeychainNotFound = keyring.ErrNotFound

var errKeychainRace = errors.New("another process is storing the key")

// KeychainAccount is the keychain account of a data directory: one entry per
// data directory, so test or second instances never share a key by accident.
func KeychainAccount(dataDir string) string {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		abs = dataDir
	}
	return "secret-key:" + filepath.Clean(abs)
}

func readKeychain(dataDir string) ([]byte, bool, error) {
	marker := filepath.Join(dataDir, KeychainMarkerName)
	if _, err := os.Stat(marker); errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", marker, err)
	}
	account := KeychainAccount(dataDir)
	// A process that just created the marker may still be storing the key.
	var (
		v   string
		err error
	)
	for range 20 {
		v, err = OSKeychain.Get(KeychainService, account)
		if !errors.Is(err, ErrKeychainNotFound) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		return nil, false, fmt.Errorf("the secret key is in the OS keychain (%s says so) but cannot be read: %w; "+
			"if it was deleted, remove %s to start with a new key and enter the connection secrets again",
			KeychainMarkerName, err, KeychainMarkerName)
	}
	key, err := decodeKey(strings.TrimSpace(v))
	if err != nil {
		return nil, false, fmt.Errorf("the secret key in the OS keychain (%s, %s) is invalid: %w", KeychainService, account, err)
	}
	return key, true, nil
}

// generateInKeychain stores a new key in the keychain. The marker is created
// first, exclusively, so two processes starting together agree on one key.
func generateInKeychain(dataDir string) ([]byte, error) {
	marker := filepath.Join(dataDir, KeychainMarkerName)
	account := KeychainAccount(dataDir)
	f, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // marker inside the configured data dir
	if errors.Is(err, os.ErrExist) {
		return nil, errKeychainRace
	}
	if err != nil {
		return nil, err
	}
	key, err := newKey()
	if err == nil {
		err = OSKeychain.Set(KeychainService, account, encodeKey(key))
	}
	if err == nil {
		_, err = fmt.Fprintf(f, "The secret key that encrypts connection secrets is stored in the OS keychain\n"+
			"(service %q, account %q).\nDo not delete this file: without it Tolarix will not look there.\n", KeychainService, account)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(marker)
		return nil, err
	}
	return key, nil
}
