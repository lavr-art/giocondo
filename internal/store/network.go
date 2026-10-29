package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	"github.com/artemlavr/giocondo/internal/singbox"
)

var WGAllocationMu sync.Mutex

type Address struct {
	ID             int64  `json:"id"`
	NodeID         int64  `json:"node_id"`
	InstallationID int64  `json:"installation_id"`
	IP             string `json:"ip"`
}
type Installation struct {
	ID          int64      `json:"id"`
	NodeID      int64      `json:"node_id"`
	Identity    string     `json:"identity"`
	IP          string     `json:"ip"`
	State       string     `json:"state"`
	LastSeen    *time.Time `json:"last_seen"`
	AppliedHash string     `json:"applied_hash"`
	ApplyError  string     `json:"apply_error"`
}
type ProbeResult struct {
	NodeID    int64     `json:"node_id"`
	IP        string    `json:"ip"`
	Tag       string    `json:"tag"`
	Point     string    `json:"point"`
	Error     string    `json:"error"`
	Failures  int       `json:"failures"`
	CheckedAt time.Time `json:"checked_at"`
}

// NetworkOperation deliberately contains no SSH credentials.
type NetworkOperation struct {
	ID              string     `json:"id"`
	NodeID          int64      `json:"node_id"`
	Kind            string     `json:"kind"`
	Status          string     `json:"status"`
	Stage           string     `json:"stage"`
	OldIP           string     `json:"old_ip"`
	NewIP           string     `json:"new_ip"`
	OldInstallation int64      `json:"old_installation"`
	NewInstallation int64      `json:"new_installation"`
	Error           string     `json:"error,omitempty"`
	Log             string     `json:"log"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`
}

func (s *SQLite) CreateAddress(ctx context.Context, a *Address) error {
	return mapErr(s.db.QueryRowContext(ctx, `INSERT INTO node_addresses(node_id,installation_id,ip) VALUES(?,?,?) RETURNING id`, a.NodeID, a.InstallationID, a.IP).Scan(&a.ID))
}
func (s *SQLite) ListAddresses(ctx context.Context, nodeID int64) ([]Address, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,node_id,installation_id,ip FROM node_addresses WHERE node_id=? ORDER BY id`, nodeID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Address{}
	for rows.Next() {
		var a Address
		if err = rows.Scan(&a.ID, &a.NodeID, &a.InstallationID, &a.IP); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *SQLite) DeleteAddress(ctx context.Context, nodeID, id int64) error {
	return affected(s.db.ExecContext(ctx, `DELETE FROM node_addresses WHERE node_id=? AND id=? AND ip<>(SELECT ip FROM nodes WHERE id=?)`, nodeID, id, nodeID))
}
func (s *SQLite) SetManagedDNS(ctx context.Context, nodeID int64, host, recordID string) error {
	return affected(s.db.ExecContext(ctx, `UPDATE nodes SET host=?,dns_record_id=?,managed_dns=TRUE WHERE id=?`, host, recordID, nodeID))
}
func (s *SQLite) CreateInstallation(ctx context.Context, i *Installation) error {
	if i.State == "" {
		i.State = "candidate"
	}
	return mapErr(s.db.QueryRowContext(ctx, `INSERT INTO node_installations(node_id,identity,ip,state) VALUES(?,?,?,?) RETURNING id`, i.NodeID, i.Identity, i.IP, i.State).Scan(&i.ID))
}
func (s *SQLite) InstallationByIdentity(ctx context.Context, identity string) (*Installation, error) {
	var i Installation
	err := s.db.QueryRowContext(ctx, `SELECT id,node_id,identity,ip,state,last_seen,applied_hash,apply_error FROM node_installations WHERE identity=?`, identity).Scan(&i.ID, &i.NodeID, &i.Identity, &i.IP, &i.State, nullTime{&i.LastSeen}, &i.AppliedHash, &i.ApplyError)
	return &i, mapErr(err)
}
func (s *SQLite) SetInstallationState(ctx context.Context, id int64, state string) error {
	return affected(s.db.ExecContext(ctx, `UPDATE node_installations SET state=? WHERE id=?`, state, id))
}
func (s *SQLite) TouchInstallation(ctx context.Context, id int64, status NodeStatus, at time.Time) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE node_installations SET last_seen=? WHERE id=?`, dbTime(at), id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE nodes SET status=?,last_seen=? WHERE active_installation=? AND status<>'disabled'`, status, dbTime(at), id)
		return err
	})
}
func (s *SQLite) ApplyInstallation(ctx context.Context, id int64, hash, applyErr string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE node_installations SET applied_hash=?,apply_error=? WHERE id=?`, hash, applyErr, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE nodes SET applied_hash=?,apply_error=? WHERE active_installation=?`, hash, applyErr, id)
		return err
	})
}
func (s *SQLite) ActivateInstallation(ctx context.Context, nodeID, id int64, ip string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_addresses WHERE node_id=? AND ip=?`, nodeID, ip).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO node_addresses(node_id,installation_id,ip) VALUES(?,?,?)`, nodeID, id, ip); err != nil {
				return mapErr(err)
			}
		} else {
			if _, err := tx.ExecContext(ctx, `UPDATE node_addresses SET installation_id=? WHERE node_id=? AND ip=?`, id, nodeID, ip); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE nodes SET active_installation=?,ip=?,down_notified=CASE WHEN ip<>? THEN FALSE ELSE down_notified END WHERE id=?`, id, ip, ip, nodeID); err != nil {
			return err
		}
		if id != 0 {
			if err := affected(tx.ExecContext(ctx, `UPDATE node_installations SET state='active' WHERE id=? AND node_id=?`, id, nodeID)); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `UPDATE nodes SET applied_hash=CASE WHEN (SELECT applied_hash FROM node_installations WHERE id=?)<>'' THEN (SELECT applied_hash FROM node_installations WHERE id=?) ELSE applied_hash END,apply_error=(SELECT apply_error FROM node_installations WHERE id=?) WHERE id=?`, id, id, id, nodeID)
			return err
		}
		return nil
	})
}
func (s *SQLite) RecordProbe(ctx context.Context, p ProbeResult) (ProbeResult, error) {
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var prev int
		err := tx.QueryRowContext(ctx, `SELECT failures FROM node_probes WHERE node_id=? AND ip=? AND tag=? AND point=?`, p.NodeID, p.IP, p.Tag, p.Point).Scan(&prev)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if p.Error != "" {
			p.Failures = prev + 1
			if p.Failures > 3 {
				p.Failures = 3
			}
		} else {
			p.Failures = 0
		}
		if err == sql.ErrNoRows {
			_, err = tx.ExecContext(ctx, `INSERT INTO node_probes(node_id,ip,tag,point,error,failures,checked_at) VALUES(?,?,?,?,?,?,?)`, p.NodeID, p.IP, p.Tag, p.Point, p.Error, p.Failures, dbTime(p.CheckedAt))
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE node_probes SET error=?,failures=?,checked_at=? WHERE node_id=? AND ip=? AND tag=? AND point=?`, p.Error, p.Failures, dbTime(p.CheckedAt), p.NodeID, p.IP, p.Tag, p.Point)
		}
		return err
	})
	return p, err
}
func (s *SQLite) ListProbes(ctx context.Context, nodeID int64) ([]ProbeResult, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT node_id,ip,tag,point,error,failures,checked_at FROM node_probes WHERE node_id=? ORDER BY ip,point,tag`, nodeID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []ProbeResult{}
	for rows.Next() {
		var p ProbeResult
		var at dbTime
		if err = rows.Scan(&p.NodeID, &p.IP, &p.Tag, &p.Point, &p.Error, &p.Failures, &at); err != nil {
			return nil, err
		}
		p.CheckedAt = time.Time(at)
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *SQLite) MonitorCredentials(ctx context.Context, nodeID int64) (*singbox.User, error) {
	var data string
	if err := s.db.QueryRowContext(ctx, `SELECT credentials FROM node_monitors WHERE node_id=?`, nodeID).Scan(&data); err != nil {
		return nil, mapErr(err)
	}
	var u singbox.User
	err := json.Unmarshal([]byte(data), &u)
	return &u, err
}
func (s *SQLite) SaveMonitorCredentials(ctx context.Context, nodeID int64, u singbox.User) error {
	b, err := json.Marshal(u)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO node_monitors(node_id,credentials) VALUES(?,?)`, nodeID, string(b))
	return mapErr(err)
}
func (s *SQLite) CreateNetworkOperation(ctx context.Context, o *NetworkOperation) error {
	o.Status = "running"
	o.StartedAt = now()
	data, err := json.Marshal(o)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO network_locks(node_id,operation_id) VALUES(?,?)`, o.NodeID, o.ID); err != nil {
			return mapErr(err)
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO network_operations(id,node_id,status,started_at,payload) VALUES(?,?,?,?,?)`, o.ID, o.NodeID, o.Status, dbTime(o.StartedAt), string(data))
		return mapErr(err)
	})
}
func (s *SQLite) SaveNetworkOperation(ctx context.Context, o *NetworkOperation) error {
	data, err := json.Marshal(o)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if err := affected(tx.ExecContext(ctx, `UPDATE network_operations SET status=?,payload=? WHERE id=?`, o.Status, string(data), o.ID)); err != nil {
			return err
		}
		if o.Status != "running" {
			_, err = tx.ExecContext(ctx, `DELETE FROM network_locks WHERE node_id=? AND operation_id=?`, o.NodeID, o.ID)
		}
		return err
	})
}
func (s *SQLite) GetNetworkOperation(ctx context.Context, id string) (*NetworkOperation, error) {
	var data string
	if err := s.db.QueryRowContext(ctx, `SELECT payload FROM network_operations WHERE id=?`, id).Scan(&data); err != nil {
		return nil, mapErr(err)
	}
	var o NetworkOperation
	err := json.Unmarshal([]byte(data), &o)
	return &o, err
}
func (s *SQLite) ListNetworkOperations(ctx context.Context, nodeID int64) ([]NetworkOperation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM network_operations WHERE (?=0 OR node_id=?) ORDER BY started_at DESC,id DESC`, nodeID, nodeID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []NetworkOperation{}
	for rows.Next() {
		var data string
		var o NetworkOperation
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(data), &o); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
func (s *SQLite) NetworkLocked(ctx context.Context, nodeID int64) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM network_locks WHERE node_id=?`, nodeID).Scan(&count)
	return count > 0, err
}

func (s *SQLite) Installations(ctx context.Context, nodeID int64) ([]Installation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,node_id,identity,ip,state,last_seen,applied_hash,apply_error FROM node_installations WHERE node_id=? ORDER BY id`, nodeID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Installation{}
	for rows.Next() {
		var i Installation
		if err = rows.Scan(&i.ID, &i.NodeID, &i.Identity, &i.IP, &i.State, nullTime{&i.LastSeen}, &i.AppliedHash, &i.ApplyError); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}
