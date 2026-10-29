package store

import (
	"database/sql/driver"
	"fmt"
	"time"
)

// timeLayout has a fixed width, UTC, seconds: strings compare the same way as instants,
// so `expires_at > ?` works in SQLite and Postgres parses this format into TIMESTAMP.
const (
	timeLayout = "2006-01-02T15:04:05Z"
	dayLayout  = "2006-01-02"
)

// dbTime is a database time. It is written as a timeLayout string and read from a string or time.Time.
type dbTime time.Time

// Value implements driver.Valuer.
func (t dbTime) Value() (driver.Value, error) {
	return time.Time(t).UTC().Format(timeLayout), nil
}

// Scan implements sql.Scanner.
func (t *dbTime) Scan(src any) error {
	switch v := src.(type) {
	case time.Time:
		*t = dbTime(v.UTC())
		return nil
	case string:
		return t.parse(v)
	case []byte:
		return t.parse(string(v))
	default:
		return fmt.Errorf("dbTime: unsupported type %T", src)
	}
}

func (t *dbTime) parse(s string) error {
	for _, layout := range []string{timeLayout, time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05"} {
		if v, err := time.Parse(layout, s); err == nil {
			*t = dbTime(v.UTC())
			return nil
		}
	}
	return fmt.Errorf("dbTime: cannot parse %q", s)
}

// nullTime is a nullable wrapper for *time.Time.
type nullTime struct{ t **time.Time }

func (n nullTime) Scan(src any) error {
	if src == nil {
		*n.t = nil
		return nil
	}
	var v dbTime
	if err := v.Scan(src); err != nil {
		return err
	}
	tt := time.Time(v)
	*n.t = &tt
	return nil
}

// nullableTime returns the value to write for *time.Time.
func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return dbTime(*t)
}

func day(t time.Time) string { return t.UTC().Format(dayLayout) }

// now is the current time truncated to seconds, as stored in the database.
func now() time.Time { return time.Now().UTC().Truncate(time.Second) }
