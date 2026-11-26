// Package secrets encrypts connection secrets at rest and provides a String
// type that refuses to print its value.
//
// Key source, in order: TOLARIX_SECRET_KEY (base64 of 32 random bytes), the
// key file "secret.key" in the data directory, then the OS keychain when the
// data directory says the key is there (see keychain.go). A missing key is
// generated on first use: in the keychain when TOLARIX_SECRET_KEY_STORE is
// "keychain" (the desktop app sets it) and the keychain works, otherwise in
// the key file. Losing the key means every stored secret has to be entered
// again.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvKey is the environment variable that supplies the master key.
const EnvKey = "TOLARIX_SECRET_KEY"

// KeyFileName is the auto-generated key file inside the data directory.
const KeyFileName = "secret.key"

const (
	keySize = 32
	format1 = byte(1) // version byte: AES-256-GCM, 12-byte nonce
)

// ErrDecrypt is returned when a ciphertext cannot be opened: wrong key, or tampered data.
var ErrDecrypt = errors.New("cannot decrypt secret: the secret key changed or the data is corrupted; re-enter the secret")

// KeySource says where the key came from (for a startup log line; never the key itself).
type KeySource string

const (
	KeyFromEnv       KeySource = "env"
	KeyFromFile      KeySource = "file"
	KeyFromKeychain  KeySource = "keychain"
	KeyFromGenerated KeySource = "generated" // new key, in the key file or the keychain (see Key.Stored)
)

// Key is a loaded master key.
type Key struct {
	Bytes  []byte
	Source KeySource
	// Stored is where a generated key was saved: KeyFromFile or KeyFromKeychain.
	Stored KeySource
	// Warning explains a fallback (keychain asked for but not available).
	Warning string
}

// Cipher seals and opens secrets with AES-256-GCM.
type Cipher struct{ aead cipher.AEAD }

// NewCipher builds a Cipher from a 32-byte key.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("secret key must be %d bytes, got %d", keySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// Seal encrypts plaintext. aad binds the ciphertext to its owner (e.g. the
// connection id) so a ciphertext copied to another row will not decrypt.
func (c *Cipher) Seal(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, 1+len(nonce)+len(plaintext)+c.aead.Overhead())
	out = append(out, format1)
	out = append(out, nonce...)
	return c.aead.Seal(out, nonce, plaintext, aad), nil
}

// Open decrypts a value produced by Seal with the same aad.
func (c *Cipher) Open(ciphertext, aad []byte) ([]byte, error) {
	ns := c.aead.NonceSize()
	if len(ciphertext) < 1+ns+c.aead.Overhead() || ciphertext[0] != format1 {
		return nil, ErrDecrypt
	}
	nonce := ciphertext[1 : 1+ns]
	pt, err := c.aead.Open(nil, nonce, ciphertext[1+ns:], aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

// LoadKey returns the master key, generating and storing one when there is
// none yet (see the package comment for the order).
func LoadKey(dataDir string) (Key, error) {
	if v := strings.TrimSpace(os.Getenv(EnvKey)); v != "" {
		key, err := decodeKey(v)
		if err != nil {
			return Key{}, fmt.Errorf("%s is invalid: %w (generate one with: openssl rand -base64 32)", EnvKey, err)
		}
		return Key{Bytes: key, Source: KeyFromEnv}, nil
	}
	if key, ok, err := readKeyFile(dataDir); ok || err != nil {
		return Key{Bytes: key, Source: KeyFromFile}, err
	}
	if key, ok, err := readKeychain(dataDir); ok || err != nil {
		return Key{Bytes: key, Source: KeyFromKeychain}, err
	}
	var warning string
	if strings.EqualFold(strings.TrimSpace(os.Getenv(EnvKeyStore)), "keychain") {
		key, err := generateInKeychain(dataDir)
		switch {
		case err == nil:
			return Key{Bytes: key, Source: KeyFromGenerated, Stored: KeyFromKeychain}, nil
		case errors.Is(err, errKeychainRace):
			return LoadKey(dataDir) // another process stored one first: use it
		default:
			warning = "the OS keychain is not available (" + err.Error() + "); the secret key is stored in " + KeyFileName + " instead"
		}
	}
	key, err := generateKeyFile(dataDir)
	if errors.Is(err, os.ErrExist) {
		return LoadKey(dataDir)
	}
	if err != nil {
		return Key{}, err
	}
	return Key{Bytes: key, Source: KeyFromGenerated, Stored: KeyFromFile, Warning: warning}, nil
}

func readKeyFile(dataDir string) ([]byte, bool, error) {
	path := filepath.Join(dataDir, KeyFileName)
	b, err := os.ReadFile(path) //nolint:gosec // path is the key file inside the configured data dir
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("read key file %s: %w", path, err)
	}
	key, err := decodeKey(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, false, fmt.Errorf("key file %s is invalid: %w", path, err)
	}
	return key, true, nil
}

// generateKeyFile creates the key file; os.ErrExist when another process
// (CLI vs server) created it first.
func generateKeyFile(dataDir string) ([]byte, error) {
	key, err := newKey()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, KeyFileName)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // key file inside the configured data dir
	if errors.Is(err, os.ErrExist) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("create key file %s: %w", path, err)
	}
	if _, err := f.WriteString(encodeKey(key) + "\n"); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("write key file %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return key, nil
}

func newKey() ([]byte, error) {
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

func encodeKey(key []byte) string { return base64.StdEncoding.EncodeToString(key) }

func decodeKey(s string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(s)
	}
	if err != nil {
		return nil, errors.New("not valid base64")
	}
	if len(key) != keySize {
		return nil, fmt.Errorf("must decode to %d bytes, got %d", keySize, len(key))
	}
	return key, nil
}
