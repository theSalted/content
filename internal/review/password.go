// Package review is the native single-operator draft UI, not Ghost session API compatibility.
package review

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const passwordWork = 600000

func HashPassword(password string) (string, error) {
	if len(password) < 12 || len(password) > 1024 || strings.ContainsRune(password, 0) {
		return "", errors.New("password must contain 12 to 1024 bytes without NUL")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, passwordWork, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", passwordWork, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

type passwordDigest struct {
	work      int
	salt, key []byte
}

func parseDigest(value string) (passwordDigest, error) {
	fail := errors.New("invalid review password hash; use content hash-password")
	p := strings.Split(value, "$")
	if len(p) != 4 || p[0] != "pbkdf2-sha256" {
		return passwordDigest{}, fail
	}
	work, err := strconv.Atoi(p[1])
	if err != nil || work < passwordWork || work > 2000000 {
		return passwordDigest{}, fail
	}
	salt, e1 := base64.RawStdEncoding.DecodeString(p[2])
	key, e2 := base64.RawStdEncoding.DecodeString(p[3])
	if e1 != nil || e2 != nil || len(salt) != 16 || len(key) != 32 {
		return passwordDigest{}, fail
	}
	return passwordDigest{work, salt, key}, nil
}
func (d passwordDigest) matches(password string) bool {
	if len(password) > 1024 {
		return false
	}
	key, err := pbkdf2.Key(sha256.New, password, d.salt, d.work, 32)
	return err == nil && subtle.ConstantTimeCompare(key, d.key) == 1
}
func equal(a, b string) bool {
	x, y := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(x[:], y[:]) == 1
}
