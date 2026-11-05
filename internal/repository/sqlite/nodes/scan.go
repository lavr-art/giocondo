package nodes

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/protocol"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

const nodeColumns = `id, name, host, ip, region, status, runtime, last_seen, created_at,
	desired_hash, applied_hash, apply_error, down_notified, dns_record_id, managed_dns, active_installation`

func scanNode(r sqlite.RowScanner) (*panel.Node, error) {
	var (
		n       panel.Node
		created sqlite.Time
	)
	if err := r.Scan(&n.ID, &n.Name, &n.Host, &n.IP, &n.Region, &n.Status, &n.Runtime,
		sqlite.NullTime(&n.LastSeen), &created,
		&n.DesiredHash, &n.AppliedHash, &n.ApplyError, &n.DownNotified, &n.DNSRecordID, &n.ManagedDNS,
		&n.ActiveInstallation); err != nil {
		return nil, sqlite.MapErr(err)
	}

	n.CreatedAt = time.Time(created)

	return &n, nil
}

type nodeWriter interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func createNode(ctx context.Context, db nodeWriter, n *panel.Node) error {
	if n.Name == "" || n.Host == "" {
		return fmt.Errorf("node name and host are required")
	}

	if n.Status == "" {
		n.Status = panel.NodeNew
	}

	if n.Runtime == "" {
		n.Runtime = panel.RuntimeRemote
	}

	n.CreatedAt = sqlite.Now()
	err := db.QueryRowContext(ctx,
		`INSERT INTO nodes (name, host, ip, region, status, runtime, last_seen, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		n.Name, n.Host, n.IP, n.Region, n.Status, n.Runtime, sqlite.NullableTime(n.LastSeen), sqlite.Time(n.CreatedAt),
	).Scan(&n.ID)

	return sqlite.MapErr(err)
}

// inboundParams is the JSON in the params column: parameters of only the protocol named in type.
func inboundParams(in protocol.Inbound) (string, error) {
	var v any

	switch in.Type {
	case protocol.TypeVLESSReality:
		v = in.Reality
	case protocol.TypeHysteria2:
		v = in.Hysteria2
	case protocol.TypeWireGuard:
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

func decodeInboundParams(in *protocol.Inbound, params string) error {
	var target any

	switch in.Type {
	case protocol.TypeVLESSReality:
		in.Reality = &protocol.RealityParams{}
		target = in.Reality
	case protocol.TypeHysteria2:
		in.Hysteria2 = &protocol.Hysteria2Params{}
		target = in.Hysteria2
	case protocol.TypeWireGuard:
		in.WireGuard = &protocol.WireGuardParams{}
		target = in.WireGuard
	default:
		return fmt.Errorf("unknown inbound type %q", in.Type)
	}

	return json.Unmarshal([]byte(params), target)
}

func scanInbound(r sqlite.RowScanner) (*panel.Inbound, error) {
	var (
		in     panel.Inbound
		params string
	)
	if err := r.Scan(&in.ID, &in.NodeID, &in.Spec.Type, &in.Spec.Tag, &in.Spec.Port, &params); err != nil {
		return nil, sqlite.MapErr(err)
	}

	if err := decodeInboundParams(&in.Spec, params); err != nil {
		return nil, fmt.Errorf("inbound %d params: %w", in.ID, err)
	}

	return &in, nil
}

func createInbound(ctx context.Context, db nodeWriter, in *panel.Inbound) error {
	if in.Spec.Tag == "" {
		in.Spec.Tag = in.Spec.EffectiveTag()
	}

	params, err := inboundParams(in.Spec)
	if err != nil {
		return err
	}

	err = db.QueryRowContext(ctx,
		`INSERT INTO inbounds (node_id, type, tag, port, params) VALUES (?, ?, ?, ?, ?) RETURNING id`,
		in.NodeID, in.Spec.Type, in.Spec.Tag, in.Spec.Port, params).Scan(&in.ID)

	return sqlite.MapErr(err)
}
