// Package migrations holds the SQL schema migrations of the panel database. Files are named
// NNNN_*.sql and applied in order; an applied migration is never edited.
package migrations

import "embed"

// FS contains the migration files at its root.
//
//go:embed *.sql
var FS embed.FS
