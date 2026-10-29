package store

import (
	"context"
	"fmt"
)

// Host is an inbound address for the subscription instead of the node host.
type Host struct {
	ID        int64
	InboundID int64
	Address   string // domain or IP
	Port      uint16 // 0 means the inbound port
	SNI       string // empty means the protocol default
	Path      string // for ws/grpc transports; unused by the current protocols
	Sort      int
}

const hostColumns = `id, inbound_id, address, port, sni, path, sort`

func scanHost(r rowScanner) (*Host, error) {
	var h Host
	if err := r.Scan(&h.ID, &h.InboundID, &h.Address, &h.Port, &h.SNI, &h.Path, &h.Sort); err != nil {
		return nil, mapErr(err)
	}
	return &h, nil
}

// CreateHost adds an inbound address.
func (s *SQLite) CreateHost(ctx context.Context, h *Host) error {
	if h.Address == "" {
		return fmt.Errorf("host address is required")
	}
	err := s.db.QueryRowContext(ctx, `INSERT INTO hosts (inbound_id, address, port, sni, path, sort)
		VALUES (?, ?, ?, ?, ?, ?) RETURNING id`, h.InboundID, h.Address, h.Port, h.SNI, h.Path, h.Sort).Scan(&h.ID)
	return mapErr(err)
}

// GetHost returns an address by id.
func (s *SQLite) GetHost(ctx context.Context, id int64) (*Host, error) {
	return scanHost(s.db.QueryRowContext(ctx, `SELECT `+hostColumns+` FROM hosts WHERE id = ?`, id))
}

// ListHosts returns all addresses ordered by inbound, sort, id.
func (s *SQLite) ListHosts(ctx context.Context) ([]Host, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+hostColumns+` FROM hosts ORDER BY inbound_id, sort, id`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer func() { _ = rows.Close() }()
	var out []Host
	for rows.Next() {
		h, err := scanHost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *h)
	}
	return out, rows.Err()
}

// UpdateHost stores the address, port, SNI, path and order. Moving it to another inbound is not allowed.
func (s *SQLite) UpdateHost(ctx context.Context, h *Host) error {
	return affected(s.db.ExecContext(ctx, `UPDATE hosts SET address = ?, port = ?, sni = ?, path = ?, sort = ? WHERE id = ?`,
		h.Address, h.Port, h.SNI, h.Path, h.Sort, h.ID))
}

// DeleteHost deletes an address.
func (s *SQLite) DeleteHost(ctx context.Context, id int64) error {
	return affected(s.db.ExecContext(ctx, `DELETE FROM hosts WHERE id = ?`, id))
}
