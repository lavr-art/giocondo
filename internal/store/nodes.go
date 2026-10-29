package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/artemlavr/giocondo/internal/singbox"
)

const nodeColumns = `id, name, host, ip, region, status, runtime, last_seen, created_at,
	desired_hash, applied_hash, apply_error, down_notified, dns_record_id, managed_dns, active_installation`

func scanNode(r rowScanner) (*Node, error) {
	var (
		n       Node
		created dbTime
	)
	if err := r.Scan(&n.ID, &n.Name, &n.Host, &n.IP, &n.Region, &n.Status, &n.Runtime, nullTime{&n.LastSeen}, &created,
		&n.DesiredHash, &n.AppliedHash, &n.ApplyError, &n.DownNotified, &n.DNSRecordID, &n.ManagedDNS, &n.ActiveInstallation); err != nil {
		return nil, mapErr(err)
	}
	n.CreatedAt = time.Time(created)
	return &n, nil
}

// CreateNode registers a node. An empty Status becomes NodeNew, an empty Runtime becomes RuntimeRemote.
func (s *SQLite) CreateNode(ctx context.Context, n *Node) error {
	if n.Name == "" || n.Host == "" {
		return fmt.Errorf("node name and host are required")
	}
	if n.Status == "" {
		n.Status = NodeNew
	}
	if n.Runtime == "" {
		n.Runtime = RuntimeRemote
	}
	n.CreatedAt = now()
	err := s.db.QueryRowContext(ctx, `INSERT INTO nodes (name, host, ip, region, status, runtime, last_seen, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		n.Name, n.Host, n.IP, n.Region, n.Status, n.Runtime, nullableTime(n.LastSeen), dbTime(n.CreatedAt)).Scan(&n.ID)
	return mapErr(err)
}

// GetNode returns a node by id.
func (s *SQLite) GetNode(ctx context.Context, id int64) (*Node, error) {
	return scanNode(s.db.QueryRowContext(ctx, `SELECT `+nodeColumns+` FROM nodes WHERE id = ?`, id))
}

// ListNodes returns all nodes in ascending id order.
func (s *SQLite) ListNodes(ctx context.Context) ([]Node, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+nodeColumns+` FROM nodes ORDER BY id`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer func() { _ = rows.Close() }()
	var out []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// UpdateNode stores name, host, ip, region and status.
func (s *SQLite) UpdateNode(ctx context.Context, n *Node) error {
	return affected(s.db.ExecContext(ctx, `UPDATE nodes SET name = ?, host = ?, ip = ?, region = ?, status = ? WHERE id = ?`,
		n.Name, n.Host, n.IP, n.Region, n.Status, n.ID))
}

// DeleteNode deletes a node together with its inbound profiles and traffic.
func (s *SQLite) DeleteNode(ctx context.Context, id int64) error {
	return affected(s.db.ExecContext(ctx, `DELETE FROM nodes WHERE id = ?`, id))
}

// TouchNode updates the status and the time of the last contact with the agent.
func (s *SQLite) TouchNode(ctx context.Context, id int64, status NodeStatus, seenAt time.Time) error {
	return affected(s.db.ExecContext(ctx, `UPDATE nodes SET status = ?, last_seen = ? WHERE id = ?`, status, dbTime(seenAt), id))
}

// SetNodeDesired records the hash of the config the panel built for the node.
func (s *SQLite) SetNodeDesired(ctx context.Context, id int64, hash string) error {
	return affected(s.db.ExecContext(ctx, `UPDATE nodes SET desired_hash = ? WHERE id = ?`, hash, id))
}

// SetNodeApplied records the hash of the config on the node and the last apply error ("" means success).
func (s *SQLite) SetNodeApplied(ctx context.Context, id int64, hash, applyErr string) error {
	return affected(s.db.ExecContext(ctx, `UPDATE nodes SET applied_hash = ?, apply_error = ? WHERE id = ?`, hash, applyErr, id))
}

// SetNodeDownNotified marks that node_down was sent (true) or the node is back online (false).
func (s *SQLite) SetNodeDownNotified(ctx context.Context, id int64, v bool) error {
	return affected(s.db.ExecContext(ctx, `UPDATE nodes SET down_notified = ? WHERE id = ?`, v, id))
}

// inboundParams is the JSON in the params column: parameters of only the protocol named in type.
func inboundParams(in singbox.Inbound) (string, error) {
	var v any
	switch in.Type {
	case singbox.TypeVLESSReality:
		v = in.Reality
	case singbox.TypeHysteria2:
		v = in.Hysteria2
	case singbox.TypeWireGuard:
		v = in.WireGuard
	default:
		return "", fmt.Errorf("unknown inbound type %q", in.Type)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	if string(b) == "null" {
		return "{}", nil
	}
	return string(b), nil
}

func decodeInboundParams(in *singbox.Inbound, params string) error {
	var target any
	switch in.Type {
	case singbox.TypeVLESSReality:
		in.Reality = &singbox.RealityParams{}
		target = in.Reality
	case singbox.TypeHysteria2:
		in.Hysteria2 = &singbox.Hysteria2Params{}
		target = in.Hysteria2
	case singbox.TypeWireGuard:
		in.WireGuard = &singbox.WireGuardParams{}
		target = in.WireGuard
	default:
		return fmt.Errorf("unknown inbound type %q", in.Type)
	}
	return json.Unmarshal([]byte(params), target)
}

func scanInbound(r rowScanner) (*Inbound, error) {
	var (
		in     Inbound
		params string
	)
	if err := r.Scan(&in.ID, &in.NodeID, &in.Spec.Type, &in.Spec.Tag, &in.Spec.Port, &params); err != nil {
		return nil, mapErr(err)
	}
	if err := decodeInboundParams(&in.Spec, params); err != nil {
		return nil, fmt.Errorf("inbound %d params: %w", in.ID, err)
	}
	return &in, nil
}

// CreateInbound adds a protocol to a node. An empty tag is replaced with singbox.Inbound.EffectiveTag.
func (s *SQLite) CreateInbound(ctx context.Context, in *Inbound) error {
	if in.Spec.Tag == "" {
		in.Spec.Tag = in.Spec.EffectiveTag()
	}
	params, err := inboundParams(in.Spec)
	if err != nil {
		return err
	}
	err = s.db.QueryRowContext(ctx, `INSERT INTO inbounds (node_id, type, tag, port, params) VALUES (?, ?, ?, ?, ?) RETURNING id`,
		in.NodeID, in.Spec.Type, in.Spec.Tag, in.Spec.Port, params).Scan(&in.ID)
	return mapErr(err)
}

// ListInbounds returns the node protocols in ascending id order.
func (s *SQLite) ListInbounds(ctx context.Context, nodeID int64) ([]Inbound, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, node_id, type, tag, port, params FROM inbounds WHERE node_id = ? ORDER BY id`, nodeID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer func() { _ = rows.Close() }()
	var out []Inbound
	for rows.Next() {
		in, err := scanInbound(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *in)
	}
	return out, rows.Err()
}

// UpdateInbound stores the type, tag, port and parameters. Moving an inbound to another node is not allowed.
func (s *SQLite) UpdateInbound(ctx context.Context, in *Inbound) error {
	params, err := inboundParams(in.Spec)
	if err != nil {
		return err
	}
	return affected(s.db.ExecContext(ctx, `UPDATE inbounds SET type = ?, tag = ?, port = ?, params = ? WHERE id = ?`,
		in.Spec.Type, in.Spec.Tag, in.Spec.Port, params, in.ID))
}

// DeleteInbound deletes a protocol.
func (s *SQLite) DeleteInbound(ctx context.Context, id int64) error {
	return affected(s.db.ExecContext(ctx, `DELETE FROM inbounds WHERE id = ?`, id))
}
