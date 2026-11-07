// Package settings stores panel settings in the panel database.
package settings

import "github.com/artemlavr/giocondo/internal/repository/sqlite"

// Repository stores the settings domain in the panel database.
type Repository struct {
	db *sqlite.DB
}

// New returns a repository over db.
func New(db *sqlite.DB) *Repository {
	return &Repository{db: db}
}
