package panelapp

import (
	"context"
	"fmt"

	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

func openDatabase(ctx context.Context, path string) (*sqlite.DB, error) {
	st, err := sqlite.Open(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("open store %s: %w", path, err)
	}

	return st, nil
}
