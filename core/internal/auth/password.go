// Package auth implements the single-admin login used in web mode: argon2id
// password hashes, the first-start one-time password, server-side sessions,
// login rate limiting and client IP resolution behind trusted proxies.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Params are the argon2id cost parameters.
type Params struct {
	Memory  uint32 // KiB
	Time    uint32
	Threads uint8
}

// DefaultParams follow the OWASP recommendation (m=19 MiB, t=2, p=1) with
// more memory, still cheap enough for a small container.
var DefaultParams = Params{Memory: 64 * 1024, Time: 2, Threads: 2}

const (
	saltLen = 16
	keyLen  = 32
)

// MinPasswordLength is enforced when the admin chooses a password.
const MinPasswordLength = 12

// ErrMismatch means the password does not match the hash.
var ErrMismatch = errors.New("wrong password")

// HashPassword returns an encoded argon2id hash ($argon2id$v=19$m=…,t=…,p=…$salt$key).
func HashPassword(password string, p Params) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, keyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword checks password against an encoded hash in constant time.
func VerifyPassword(encoded, password string) error {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return errors.New("unsupported password hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return errors.New("unsupported argon2 version")
	}
	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return errors.New("invalid argon2 parameters")
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return errors.New("invalid salt")
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return errors.New("invalid key")
	}
	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, uint32(len(want))) //nolint:gosec // len(want) is small
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrMismatch
	}
	return nil
}

// passwordAlphabet avoids look-alike characters (0/O, 1/l/I) because the
// one-time password is read from a log and typed by hand.
const passwordAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// GeneratePassword returns a random password of 4 groups of 5 characters
// (about 116 bits), e.g. "x7Kqp-3mRtz-…".
func GeneratePassword() string {
	const groups, size = 4, 5
	var b strings.Builder
	buf := make([]byte, groups*size)
	if _, err := rand.Read(buf); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	for i, c := range buf {
		if i > 0 && i%size == 0 {
			b.WriteByte('-')
		}
		// 256 % 56 != 0 gives a negligible bias for a 116-bit password.
		b.WriteByte(passwordAlphabet[int(c)%len(passwordAlphabet)])
	}
	return b.String()
}

// CheckNewPassword validates a password chosen by the admin. The message is
// shown to the user as is.
func CheckNewPassword(pw string) error {
	if utf8.RuneCountInString(pw) < MinPasswordLength {
		return fmt.Errorf("use at least %d characters", MinPasswordLength)
	}
	if utf8.RuneCountInString(pw) > 1024 {
		return errors.New("use at most 1024 characters")
	}
	if strings.TrimSpace(pw) == "" {
		return errors.New("the password cannot be only spaces")
	}
	return nil
}

// RandomToken returns 32 random bytes, base64url encoded.
func RandomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
