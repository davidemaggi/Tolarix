package secrets

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testCipher(t *testing.T) *Cipher {
	t.Helper()
	key := make([]byte, keySize)
	_, _ = rand.Read(key)
	c, err := NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSealOpenRoundTrip(t *testing.T) {
	c := testCipher(t)
	ct, err := c.Seal([]byte("my-pat"), []byte("conn-1"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("my-pat")) {
		t.Fatal("ciphertext contains plaintext")
	}
	pt, err := c.Open(ct, []byte("conn-1"))
	if err != nil {
		t.Fatal(err)
	}
	if string(pt) != "my-pat" {
		t.Fatalf("got %q", pt)
	}
}

func TestOpenRejectsWrongAADTamperAndKey(t *testing.T) {
	c := testCipher(t)
	ct, _ := c.Seal([]byte("secret"), []byte("conn-1"))

	if _, err := c.Open(ct, []byte("conn-2")); !errors.Is(err, ErrDecrypt) {
		t.Errorf("wrong aad: got %v", err)
	}
	tampered := append([]byte(nil), ct...)
	tampered[len(tampered)-1] ^= 1
	if _, err := c.Open(tampered, []byte("conn-1")); !errors.Is(err, ErrDecrypt) {
		t.Errorf("tampered: got %v", err)
	}
	if _, err := testCipher(t).Open(ct, []byte("conn-1")); !errors.Is(err, ErrDecrypt) {
		t.Errorf("other key: got %v", err)
	}
	if _, err := c.Open([]byte{1, 2}, nil); !errors.Is(err, ErrDecrypt) {
		t.Errorf("short: got %v", err)
	}
}

func TestSealUsesFreshNonce(t *testing.T) {
	c := testCipher(t)
	a, _ := c.Seal([]byte("x"), nil)
	b, _ := c.Seal([]byte("x"), nil)
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same plaintext must differ")
	}
}

func TestLoadKeyGeneratesThenReusesFile(t *testing.T) {
	t.Setenv(EnvKey, "")
	t.Setenv(EnvKeyStore, "")
	dir := t.TempDir()
	k1, err := LoadKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if k1.Source != KeyFromGenerated || k1.Stored != KeyFromFile || len(k1.Bytes) != keySize {
		t.Fatalf("first load: %+v", k1)
	}
	k2, err := LoadKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if k2.Source != KeyFromFile || !bytes.Equal(k1.Bytes, k2.Bytes) {
		t.Fatalf("second load: src=%s equal=%v", k2.Source, bytes.Equal(k1.Bytes, k2.Bytes))
	}
}

func TestLoadKeyFromEnv(t *testing.T) {
	key := make([]byte, keySize)
	_, _ = rand.Read(key)
	t.Setenv(EnvKey, base64.StdEncoding.EncodeToString(key))
	dir := t.TempDir()
	got, err := LoadKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != KeyFromEnv || !bytes.Equal(got.Bytes, key) {
		t.Fatal("env key not used")
	}
	if _, err := os.Stat(filepath.Join(dir, KeyFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("key file must not be created when env key is set")
	}
}

func TestLoadKeyInvalidEnv(t *testing.T) {
	t.Setenv(EnvKey, "too-short")
	if _, err := LoadKey(t.TempDir()); err == nil || !strings.Contains(err.Error(), EnvKey) {
		t.Fatalf("expected helpful error, got %v", err)
	}
}

func TestStringRedacts(t *testing.T) {
	s := New("hunter2")
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("x", "token", s)
	j, _ := json.Marshal(struct{ T String }{s})
	for name, out := range map[string]string{
		"fmt %v":  fmt.Sprintf("%v", s),
		"fmt %s":  fmt.Sprintf("%s", s), //nolint:staticcheck // exercising the fmt path on purpose
		"fmt %#v": fmt.Sprintf("%#v", s),
		"fmt %+v": fmt.Sprintf("%+v", struct{ T String }{s}),
		"slog":    buf.String(),
		"json":    string(j),
	} {
		if strings.Contains(out, "hunter2") {
			t.Errorf("%s leaked secret: %s", name, out)
		}
	}
	if s.Reveal() != "hunter2" || !s.IsSet() {
		t.Fatal("Reveal broken")
	}
	var in struct{ T String }
	if err := json.Unmarshal([]byte(`{"T":"abc"}`), &in); err != nil || in.T.Reveal() != "abc" {
		t.Fatalf("unmarshal: %v %q", err, in.T.Reveal())
	}
}
