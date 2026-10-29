package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/artemlavr/giocondo/internal/store"
)

// API token scopes. Each one includes the previous ones: admin ⊃ node-sync ⊃ monitor.
const (
	ScopeMonitor  = "monitor"   // read only
	ScopeNodeSync = "node-sync" // read + nodes, host overrides, agent certificates
	ScopeAdmin    = "admin"     // everything, including users and tokens
)

var scopeRank = map[string]int{ScopeMonitor: 1, ScopeNodeSync: 2, ScopeAdmin: 3}

// ScopeAllows reports whether scope have is enough for an operation that requires need.
func ScopeAllows(have, need string) bool {
	return scopeRank[have] > 0 && scopeRank[have] >= scopeRank[need]
}

// ErrUnauthorized means the token is missing or revoked.
var ErrUnauthorized = errors.New("invalid API token")

// tokenPrefix helps recognize a panel token in logs and secret scanners.
const tokenPrefix = "vpc_"

// touchEvery is the minimum interval between last_used_at writes for one token.
const touchEvery = time.Minute

// TokenView is an API token without the secret.
type TokenView struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Scope      string     `json:"scope"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

func tokenView(t store.APIToken) TokenView {
	return TokenView{ID: t.ID, Name: t.Name, Scope: t.Scope, CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt}
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CreateToken issues a token. The token itself is returned only here: the database stores its sha256.
func (s *Service) CreateToken(ctx context.Context, name, scope string) (TokenView, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return TokenView{}, "", invalid("token name must be 1-64 characters")
	}
	if scopeRank[scope] == 0 {
		return TokenView{}, "", invalid("scope must be %s, %s or %s", ScopeMonitor, ScopeNodeSync, ScopeAdmin)
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return TokenView{}, "", err
	}
	secret := tokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	t := &store.APIToken{Name: name, Scope: scope, Hash: hashToken(secret)}
	if err := s.Store.CreateToken(ctx, t); err != nil {
		return TokenView{}, "", err
	}
	s.log().Info("api token created", "name", name, "scope", scope)
	return tokenView(*t), secret, nil
}

// ListTokens returns tokens without secrets.
func (s *Service) ListTokens(ctx context.Context) ([]TokenView, error) {
	list, err := s.Store.ListTokens(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]TokenView, len(list))
	for i, t := range list {
		out[i] = tokenView(t)
	}
	return out, nil
}

// DeleteToken revokes a token.
func (s *Service) DeleteToken(ctx context.Context, id int64) error {
	if err := s.Store.DeleteToken(ctx, id); err != nil {
		return err
	}
	s.log().Info("api token deleted", "id", id)
	return nil
}

// Authenticate checks a token and returns its scope.
func (s *Service) Authenticate(ctx context.Context, token string) (TokenView, error) {
	if !strings.HasPrefix(token, tokenPrefix) {
		return TokenView{}, ErrUnauthorized
	}
	t, err := s.Store.TokenByHash(ctx, hashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		return TokenView{}, ErrUnauthorized
	}
	if err != nil {
		return TokenView{}, err
	}
	now := s.now()
	s.touchMu.Lock()
	if s.touched == nil {
		s.touched = map[int64]time.Time{}
	}
	stale := now.Sub(s.touched[t.ID]) >= touchEvery
	if stale {
		s.touched[t.ID] = now
	}
	s.touchMu.Unlock()
	if stale {
		if err := s.Store.TouchToken(ctx, t.ID, now); err != nil {
			s.log().Warn("touch api token", "id", t.ID, "err", err)
		}
		t.LastUsedAt = &now
	}
	return tokenView(*t), nil
}
