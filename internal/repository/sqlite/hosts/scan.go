package hosts

import (
	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

const hostColumns = `id, inbound_id, address, port, sni, path, sort`

func scanHost(r sqlite.RowScanner) (*panel.Host, error) {
	var h panel.Host
	if err := r.Scan(&h.ID, &h.InboundID, &h.Address, &h.Port, &h.SNI, &h.Path, &h.Sort); err != nil {
		return nil, sqlite.MapErr(err)
	}

	return &h, nil
}
