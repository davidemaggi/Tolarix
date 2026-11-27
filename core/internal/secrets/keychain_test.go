package secrets

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type fakeKeychain struct {
	mu      sync.Mutex
	entries map[string]string
	fail    error
	gets    int
}

func (k *fakeKeychain) Get(s, a string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.gets++
	if k.fail != nil {
		return "", k.fail
	}
	v, ok := k.entries[s+"|"+a]
	if !ok {
		return "", ErrKeychainNotFound
	}
	return v, nil
}

func (k *fakeKeychain) Set(s, a, v string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.fail != nil {
		return k.fail
	}
	k.entries[s+"|"+a] = v
	return nil
}

func (k *fakeKeychain) Delete(s, a string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.entries, s+"|"+a)
	return nil
}

func useFakeKeychain(t *testing.T) *fakeKeychain {
	t.Helper()
	k := &fakeKeychain{entries: map[string]string{}}
	old := OSKeychain
	OSKeychain = k
	t.Cleanup(func() { OSKeychain = old })
	t.Setenv(EnvKey, "")
	return k
}

func TestKeychainStoresNewKeyAndOthersFindIt(t *testing.T) {
	kc := useFakeKeychain(t)
	dir := t.TempDir()
	t.Setenv(EnvKeyStore, "keychain")
	k1, err := LoadKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if k1.Source != KeyFromGenerated || k1.Stored != KeyFromKeychain || k1.Warning != "" {
		t.Fatalf("first load: %+v", k1)
	}
	if _, err := os.Stat(filepath.Join(dir, KeyFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("no key file when the key is in the keychain")
	}
	marker, err := os.ReadFile(filepath.Join(dir, KeychainMarkerName))
	if err != nil || bytes.Contains(marker, []byte(encodeKey(k1.Bytes))) {
		t.Fatalf("marker must exist and never hold the key: %v", err)
	}
	// The CLI does not set the store, and still finds the key through the marker.
	t.Setenv(EnvKeyStore, "")
	k2, err := LoadKey(dir)
	if err != nil || k2.Source != KeyFromKeychain || !bytes.Equal(k1.Bytes, k2.Bytes) {
		t.Fatalf("second load: %+v %v", k2, err)
	}
	// Another data directory has its own entry.
	other, err := LoadKey(t.TempDir())
	if err != nil || other.Stored != KeyFromFile || bytes.Equal(other.Bytes, k1.Bytes) {
		t.Fatalf("other dir: %+v %v", other, err)
	}
	if len(kc.entries) != 1 {
		t.Fatalf("entries = %v", len(kc.entries))
	}
}

func TestKeychainUnavailableFallsBackToFile(t *testing.T) {
	kc := useFakeKeychain(t)
	kc.fail = errors.New("no secret service")
	t.Setenv(EnvKeyStore, "keychain")
	dir := t.TempDir()
	k, err := LoadKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if k.Stored != KeyFromFile || !strings.Contains(k.Warning, "no secret service") {
		t.Fatalf("got %+v", k)
	}
	if _, err := os.Stat(filepath.Join(dir, KeychainMarkerName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the marker must be removed when the keychain fails")
	}
	again, err := LoadKey(dir)
	if err != nil || again.Source != KeyFromFile || !bytes.Equal(again.Bytes, k.Bytes) {
		t.Fatalf("reload: %+v %v", again, err)
	}
}

func TestKeychainNeverTouchedWithoutMarker(t *testing.T) {
	kc := useFakeKeychain(t)
	t.Setenv(EnvKeyStore, "")
	if _, err := LoadKey(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if kc.gets != 0 || len(kc.entries) != 0 {
		t.Fatal("the keychain must not be used when the key lives in a file")
	}
}

func TestKeychainEntryMissingIsExplained(t *testing.T) {
	kc := useFakeKeychain(t)
	t.Setenv(EnvKeyStore, "keychain")
	dir := t.TempDir()
	if _, err := LoadKey(dir); err != nil {
		t.Fatal(err)
	}
	kc.fail = errors.New("access denied")
	_, err := LoadKey(dir)
	if err == nil || !strings.Contains(err.Error(), "access denied") || !strings.Contains(err.Error(), KeychainMarkerName) {
		t.Fatalf("err = %v", err)
	}
}
