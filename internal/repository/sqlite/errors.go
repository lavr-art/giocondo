package sqlite

import (
	"database/sql"
	"errors"
	"fmt"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// MapErr maps driver errors to ErrConflict/ErrNotFound.
func MapErr(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, sql.ErrNoRows) {
		return panel.ErrNotFound
	}

	if se, ok := errors.AsType[*sqlite.Error](err); ok {
		switch se.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return fmt.Errorf("%w: %s", panel.ErrConflict, se.Error())
		case sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
			return fmt.Errorf("%w: referenced row: %s", panel.ErrNotFound, se.Error())
		}
	}

	return err
}

// Affected returns ErrNotFound if the query affected no rows.
func Affected(res sql.Result, err error) error {
	if err != nil {
		return MapErr(err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if n == 0 {
		return panel.ErrNotFound
	}

	return nil
}
