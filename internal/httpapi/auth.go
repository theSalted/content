package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type AdminKey struct {
	id     string
	secret []byte
}

func ParseAdminKey(value string) (AdminKey, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 || len(parts[0]) != 24 {
		return AdminKey{}, errors.New("admin key must be a 24-hex ID and a 64-hex secret separated by a colon")
	}
	if _, err := hex.DecodeString(parts[0]); err != nil {
		return AdminKey{}, errors.New("admin key ID must be hexadecimal")
	}
	secret, err := hex.DecodeString(parts[1])
	if err != nil || len(secret) != 32 {
		return AdminKey{}, errors.New("admin key secret must contain 32 hexadecimal-encoded bytes")
	}
	return AdminKey{parts[0], secret}, nil
}
func (k AdminKey) validate(header string, now time.Time) error {
	return k.validateAudience(header, now, "/admin/")
}
func (k AdminKey) validateAudience(header string, now time.Time, audience string) error {
	if !strings.HasPrefix(header, "Ghost ") || len(header) > 8192 {
		return errors.New("invalid authorization")
	}
	claims := &jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(strings.TrimPrefix(header, "Ghost "), claims, func(t *jwt.Token) (any, error) {
		if t.Header["kid"] != k.id || t.Header["typ"] != "JWT" {
			return nil, errors.New("unknown key or token type")
		}
		return k.secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience(audience), jwt.WithExpirationRequired(), jwt.WithTimeFunc(func() time.Time { return now }))
	if err != nil || !token.Valid || claims.IssuedAt == nil || claims.ExpiresAt == nil {
		return errors.New("invalid token")
	}
	// iA Writer 8 rounds its issued-at value into the next second. Bound this
	// allowance to iat only: expiry and nbf retain the JWT parser's strict checks.
	if claims.IssuedAt.Time.After(now.Add(time.Second)) {
		return errors.New("invalid issued-at time")
	}
	if claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time) > 5*time.Minute || !claims.ExpiresAt.After(claims.IssuedAt.Time) || now.Sub(claims.IssuedAt.Time) > 5*time.Minute {
		return errors.New("invalid token lifetime")
	}
	return nil
}
func equalKey(a, b string) bool {
	aa, bb := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(aa[:], bb[:]) == 1
}
