package tokens

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// ErrUnauthorized means the token is missing or revoked.
var ErrUnauthorized = errors.New("invalid API token")

// tokenPrefix helps recognize a panel token in logs and secret scanners.
const tokenPrefix = "vpc_"

// touchEvery is the minimum interval between last_used_at writes for one token.
const touchEvery = time.Minute

func tokenView(t panel.APIToken) panel.TokenView {
	return panel.TokenView{ID: t.ID, Name: t.Name, Scope: t.Scope, CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt}
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))

	return hex.EncodeToString(sum[:])
}
