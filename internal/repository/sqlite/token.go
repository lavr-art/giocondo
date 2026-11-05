package sqlite

import (
	"crypto/rand"
	"encoding/base64"
)

// NewToken generates a subscription token: 32 random bytes in unpadded base64url (43 characters).
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}
