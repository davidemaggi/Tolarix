// Package secrets encrypts connection secrets at rest and provides a String
// type that refuses to print its value.
//
// Key source, in order: TOLARIX_SECRET_KEY (base64 of 32 random bytes), then
// the key file "secret.key" in the data directory, generated on first use.
// Losing the key means every stored secret has to be entered again.
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
	KeyFromGenerated KeySource = "generated"
)

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

// LoadKey returns the master key and where it came from, creating the key
// file in dataDir when neither the env var nor the file exists.
func LoadKey(dataDir string) ([]byte, KeySource, error) {
	if v := strings.TrimSpace(os.Getenv(EnvKey)); v != "" {
		key, err := decodeKey(v)
		if err != nil {
			return nil, "", fmt.Errorf("%s is invalid: %w (generate one with: openssl rand -base64 32)", EnvKey, err)
		}
		return key, KeyFromEnv, nil
	}
	path := filepath.Join(dataDir, KeyFileName)
	b, err := os.ReadFile(path) //nolint:gosec // path is the key file inside the configured data dir
	switch {
	case err == nil:
		key, derr := decodeKey(strings.TrimSpace(string(b)))
		if derr != nil {
			return nil, "", fmt.Errorf("key file %s is invalid: %w", path, derr)
		}
		return key, KeyFromFile, nil
	case !errors.Is(err, os.ErrNotExist):
		return nil, "", fmt.Errorf("read key file %s: %w", path, err)
	}
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, "", err
	}
	// O_EXCL: if another process (CLI vs server) created it first, use theirs.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // key file inside the configured data dir
	if errors.Is(err, os.ErrExist) {
		return LoadKey(dataDir)
	}
	if err != nil {
		return nil, "", fmt.Errorf("create key file %s: %w", path, err)
	}
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(key) + "\n"); err != nil {
		_ = f.Close()
		return nil, "", fmt.Errorf("write key file %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return nil, "", err
	}
	return key, KeyFromGenerated, nil
}

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
