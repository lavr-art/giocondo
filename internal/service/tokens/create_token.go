package tokens

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"strings"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/operation"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// CreateToken issues a token. The token itself is returned only here: the database stores its sha256.
func (s *Service) CreateToken(ctx context.Context, name, scope string) (panel.TokenView, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return panel.TokenView{}, "", operation.Invalid("token name must be 1-64 characters")
	}

	if scopeRank[scope] == 0 {
		return panel.TokenView{}, "", operation.Invalid(
			"scope must be %s, %s or %s",
			ScopeMonitor,
			ScopeNodeSync,
			ScopeAdmin,
		)
	}

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return panel.TokenView{}, "", err
	}

	secret := tokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	t := &panel.APIToken{Name: name, Scope: scope, Hash: hashToken(secret)}

	if err := s.tokens.CreateToken(ctx, t); err != nil {
		return panel.TokenView{}, "", err
	}

	logger.FromCtx(ctx).Info("api token created", "name", name, "scope", scope)

	return tokenView(*t), secret, nil
}
