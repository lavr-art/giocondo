package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/artemlavr/giocondo/internal/singbox"
)

// Store errors.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("already exists") // uniqueness violation
)

// NodeStatus is the node state.
type NodeStatus string

// Node states.
const (
	NodeNew      NodeStatus = "new"
	NodeOnline   NodeStatus = "online"
	NodeOffline  NodeStatus = "offline"
	NodeDisabled NodeStatus = "disabled"
)

// User is a VPN user.
type User struct {
	ID                int64
	Name              string
	Token             string // subscription token, /sub/<token>
	UUID              string
	TrafficLimitBytes int64      // 0 = no limit
	ExpiresAt         *time.Time // nil = no expiry
	Enabled           bool
	CreatedAt         time.Time
	WG                *singbox.WGPeer // nil until WireGuard keys are issued
	UsageOffset       Usage           // traffic before the last ResetUsage; changed only by it
	Watch             UserWatch       // watcher state; changed only by SetUserWatch
}

// UserWatch is what the panel background watcher has already seen and warned about.
type UserWatch struct {
	LastStatus    string // status at the previous check; "" means not checked yet
	WarnedTraffic bool   // the 80% traffic warning was sent
	WarnedExpiry  bool   // the approaching expiry warning was sent
}

// NodeRuntime is where the node agent runs.
type NodeRuntime string

// NodeRuntime values.
const (
	RuntimeRemote NodeRuntime = "remote" // a separate server
	RuntimeLocal  NodeRuntime = "local"  // agent on the panel machine
)

// Node is a VPS with the agent and sing-box.
type Node struct {
	ID        int64
	Name      string
	Host      string // domain or IP clients connect to
	IP        string
	Region    string
	Status    NodeStatus
	Runtime   NodeRuntime // set at creation; empty means RuntimeRemote
	LastSeen  *time.Time
	CreatedAt time.Time
	// Service fields: UpdateNode does not write them, they change through SetNode*.
	DesiredHash        string // sha256 of the config built by the panel
	AppliedHash        string // sha256 of the config on the node
	ApplyError         string // last apply error; "" means applied
	DNSRecordID        string
	ManagedDNS         bool
	ActiveInstallation int64
	DownNotified       bool // node_down was sent
}

// Dirty means the node runs a different config than the panel built.
func (n Node) Dirty() bool { return n.DesiredHash != "" && n.DesiredHash != n.AppliedHash }

// APIToken is a REST API token. The token itself is not stored, only its sha256.
type APIToken struct {
	ID         int64
	Name       string
	Scope      string
	Hash       string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// Inbound is a protocol on a node. Spec is the config generator model; its parameters are stored as JSON.
type Inbound struct {
	ID     int64
	NodeID int64
	Spec   singbox.Inbound
}

// TrafficDelta is a user's traffic increment on a node reported by the agent.
type TrafficDelta struct {
	UserID int64
	NodeID int64
	At     time.Time // determines the day (UTC)
	Up     int64
	Down   int64
}

// Usage is total traffic.
type Usage struct {
	Up   int64
	Down int64
}

// Total returns up + down.
func (u Usage) Total() int64 { return u.Up + u.Down }

// clamp keeps the value non-negative (the offset can exceed the sum when history was deleted with a node).
func (u Usage) clamp() Usage {
	return Usage{Up: max(u.Up, 0), Down: max(u.Down, 0)}
}

// Store is the panel storage.
type Store interface {
	CreateUser(ctx context.Context, u *User) error
	GetUser(ctx context.Context, id int64) (*User, error)
	GetUserByToken(ctx context.Context, token string) (*User, error)
	ListUsers(ctx context.Context) ([]User, error)
	UpdateUser(ctx context.Context, u *User) error
	DeleteUser(ctx context.Context, id int64) error
	RotateToken(ctx context.Context, id int64) (string, error)
	SetUserWatch(ctx context.Context, id int64, w UserWatch) error

	CreateNode(ctx context.Context, n *Node) error
	GetNode(ctx context.Context, id int64) (*Node, error)
	ListNodes(ctx context.Context) ([]Node, error)
	UpdateNode(ctx context.Context, n *Node) error
	DeleteNode(ctx context.Context, id int64) error
	TouchNode(ctx context.Context, id int64, status NodeStatus, seenAt time.Time) error
	SetNodeDesired(ctx context.Context, id int64, hash string) error
	SetNodeApplied(ctx context.Context, id int64, hash, applyErr string) error
	SetNodeDownNotified(ctx context.Context, id int64, v bool) error

	CreateAddress(context.Context, *Address) error
	ListAddresses(context.Context, int64) ([]Address, error)
	DeleteAddress(context.Context, int64, int64) error
	SetManagedDNS(context.Context, int64, string, string) error
	Installations(context.Context, int64) ([]Installation, error)
	CreateInstallation(context.Context, *Installation) error
	InstallationByIdentity(context.Context, string) (*Installation, error)
	SetInstallationState(context.Context, int64, string) error
	TouchInstallation(context.Context, int64, NodeStatus, time.Time) error
	ApplyInstallation(context.Context, int64, string, string) error
	ActivateInstallation(context.Context, int64, int64, string) error
	RecordProbe(context.Context, ProbeResult) (ProbeResult, error)
	ListProbes(context.Context, int64) ([]ProbeResult, error)
	MonitorCredentials(context.Context, int64) (*singbox.User, error)
	SaveMonitorCredentials(context.Context, int64, singbox.User) error
	CreateNetworkOperation(context.Context, *NetworkOperation) error
	SaveNetworkOperation(context.Context, *NetworkOperation) error
	GetNetworkOperation(context.Context, string) (*NetworkOperation, error)
	ListNetworkOperations(context.Context, int64) ([]NetworkOperation, error)
	NetworkLocked(context.Context, int64) (bool, error)

	CreateInbound(ctx context.Context, in *Inbound) error
	ListInbounds(ctx context.Context, nodeID int64) ([]Inbound, error)
	UpdateInbound(ctx context.Context, in *Inbound) error
	DeleteInbound(ctx context.Context, id int64) error

	CreateHost(ctx context.Context, h *Host) error
	GetHost(ctx context.Context, id int64) (*Host, error)
	ListHosts(ctx context.Context) ([]Host, error)
	UpdateHost(ctx context.Context, h *Host) error
	DeleteHost(ctx context.Context, id int64) error

	AddTraffic(ctx context.Context, deltas []TrafficDelta) error
	UsageByUser(ctx context.Context, userID int64, from, to time.Time) (Usage, error)
	UserUsage(ctx context.Context, userID int64) (Usage, error)
	UsageAll(ctx context.Context) (map[int64]Usage, error)
	ResetUsage(ctx context.Context, userID int64) error
	ActiveUsers(ctx context.Context, now time.Time) ([]User, error)

	CreateToken(ctx context.Context, t *APIToken) error
	ListTokens(ctx context.Context) ([]APIToken, error)
	TokenByHash(ctx context.Context, hash string) (*APIToken, error)
	TouchToken(ctx context.Context, id int64, at time.Time) error
	DeleteToken(ctx context.Context, id int64) error

	Close() error
}

//go:embed migrations/*.sql
var migrationsFS embed.FS

// SQLite is the SQLite implementation of Store.
type SQLite struct {
	db *sql.DB
}

var _ Store = (*SQLite)(nil)

// Open opens (or creates) the database and applies migrations.
func Open(ctx context.Context, path string) (*SQLite, error) {
	// Check existing databases read-only, before journal_mode or other connection pragmas can write.
	if _, err := os.Stat(path); err == nil {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		uri := url.URL{Scheme: "file", Path: absolute, RawQuery: "mode=ro"}
		existing, err := sql.Open("sqlite", uri.String())
		if err != nil {
			return nil, err
		}
		err = (&SQLite{db: existing}).checkSchema(ctx)
		closeErr := existing.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	q := url.Values{}
	for _, p := range []string{"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(5000)", "synchronous(NORMAL)"} {
		q.Add("_pragma", p)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	// One connection: writes are serialized anyway, and pragmas apply to everything at once.
	db.SetMaxOpenConns(1)
	s := &SQLite{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *SQLite) Close() error { return s.db.Close() }

func (s *SQLite) checkSchema(ctx context.Context) error {
	names, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	// Reject a newer application's database before executing any migration.
	var tableExists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&tableExists); err != nil {
		return err
	}
	if tableExists != 0 {
		var current int
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
			return err
		}
		latest, _, _ := strings.Cut(strings.TrimPrefix(names[len(names)-1], "migrations/"), "_")
		maximum, err := strconv.Atoi(latest)
		if err != nil {
			return err
		}
		if current > maximum {
			return fmt.Errorf("database schema %d is newer than supported schema %d; restore matching packages and data", current, maximum)
		}
	}
	return nil
}

func (s *SQLite) migrate(ctx context.Context) error {
	names, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	if err := s.checkSchema(ctx); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TIMESTAMP NOT NULL
	)`); err != nil {
		return fmt.Errorf("migrations table: %w", err)
	}
	for _, name := range names {
		base := strings.TrimPrefix(name, "migrations/")
		num, _, _ := strings.Cut(base, "_")
		version, err := strconv.Atoi(num)
		if err != nil {
			return fmt.Errorf("migration %s: name must start with a number", base)
		}
		var exists int
		err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&exists)
		if err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		body, err := migrationsFS.ReadFile(name)
		if err != nil {
			return err
		}
		if err := s.inTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, string(body)); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
				version, dbTime(time.Now()))
			return err
		}); err != nil {
			return fmt.Errorf("migration %s: %w", base, err)
		}
	}
	return nil
}

func (s *SQLite) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// NewToken generates a subscription token: 32 random bytes in unpadded base64url (43 characters).
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// mapErr maps driver errors to ErrConflict/ErrNotFound.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return fmt.Errorf("%w: %s", ErrConflict, se.Error())
		case sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
			return fmt.Errorf("%w: referenced row: %s", ErrNotFound, se.Error())
		}
	}
	return err
}

// affected returns ErrNotFound if the query affected no rows.
func affected(res sql.Result, err error) error {
	if err != nil {
		return mapErr(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
