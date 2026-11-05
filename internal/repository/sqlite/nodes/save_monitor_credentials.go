package nodes

import (
	"context"
	"encoding/json"

	"github.com/artemlavr/giocondo/internal/model/protocol"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// SaveMonitorCredentials stores monitor credentials.
func (r *Repository) SaveMonitorCredentials(ctx context.Context, nodeID int64, u protocol.User) error {
	b, err := json.Marshal(u)
	if err != nil {
		return err
	}

	_, err = r.db.ExecContext(ctx, `INSERT INTO node_monitors(node_id,credentials) VALUES(?,?)`, nodeID, string(b))

	return sqlite.MapErr(err)
}
