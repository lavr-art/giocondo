package nodes

import (
	"context"
	"encoding/json"

	"github.com/artemlavr/giocondo/internal/model/protocol"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// MonitorCredentials returns the monitor credentials.
func (r *Repository) MonitorCredentials(ctx context.Context, nodeID int64) (*protocol.User, error) {
	var data string
	if err := r.db.QueryRowContext(ctx,
		`SELECT credentials FROM node_monitors WHERE node_id=?`,
		nodeID).Scan(&data); err != nil {
		return nil, sqlite.MapErr(err)
	}

	var u protocol.User

	err := json.Unmarshal([]byte(data), &u)

	return &u, err
}
