package sqlite

import (
	"database/sql"
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

// Time is a database time. It is written as a timeLayout string and read from a string or time.Time.
//
//nolint:recvcheck // driver.Valuer needs a value receiver for Time arguments, sql.Scanner a pointer one.
type Time time.Time

// Value implements driver.Valuer.
func (t Time) Value() (driver.Value, error) {
	return time.Time(t).UTC().Format(timeLayout), nil
}

// Scan implements sql.Scanner.
func (t *Time) Scan(src any) error {
	switch v := src.(type) {
	case time.Time:
		*t = Time(v.UTC())

		return nil
	case string:
		return t.parse(v)
	case []byte:
		return t.parse(string(v))
	default:
		return fmt.Errorf("sqlite.Time: unsupported type %T", src)
	}
}

func (t *Time) parse(s string) error {
	for _, layout := range []string{
		timeLayout,
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05",
	} {
		if v, err := time.Parse(layout, s); err == nil {
			*t = Time(v.UTC())

			return nil
		}
	}

	return fmt.Errorf("sqlite.Time: cannot parse %q", s)
}

// NullTime scans a nullable database time into *t; NULL stores nil.
func NullTime(t **time.Time) sql.Scanner { return nullTime{t} }

type nullTime struct{ t **time.Time }

func (n nullTime) Scan(src any) error {
	if src == nil {
		*n.t = nil

		return nil
	}

	var v Time
	if err := v.Scan(src); err != nil {
		return err
	}

	tt := time.Time(v)
	*n.t = &tt

	return nil
}

// NullableTime returns the value to write for *time.Time.
func NullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}

	return Time(*t)
}

// Day is the traffic day of t: YYYY-MM-DD in UTC.
func Day(t time.Time) string { return t.UTC().Format(dayLayout) }

// Now is the current time truncated to seconds, as stored in the database.
func Now() time.Time { return time.Now().UTC().Truncate(time.Second) }
