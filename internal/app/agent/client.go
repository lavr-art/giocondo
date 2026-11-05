package agent

import (
	"errors"
	"fmt"

	"github.com/artemlavr/giocondo/internal/clients/panel"
	"github.com/artemlavr/giocondo/internal/platform/singboxctl"
	"github.com/artemlavr/giocondo/internal/repository/pkifs"
	"github.com/artemlavr/giocondo/internal/transport/grpc/v2raystats"
	"github.com/artemlavr/giocondo/internal/worker/panelsync"
)

// panelConns opens one connection per address once and caches them. grpc.NewClient is lazy: the socket
// is opened only when the agent tries this address.
func (a *App) panelConns() ([]panelsync.Panel, error) {
	if a.panels != nil {
		return a.panels, nil
	}

	if len(a.cfg.Panels) == 0 {
		return nil, errors.New("no panel addresses")
	}

	panels := make([]panelsync.Panel, 0, len(a.cfg.Panels))
	for _, p := range a.cfg.Panels {
		tlsCfg, err := pkifs.ClientTLS(a.cfg.CAFile, a.cfg.CertFile, a.cfg.KeyFile, p.ServerName)
		if err != nil {
			return nil, fmt.Errorf("tls: %w", err)
		}

		client, err := panel.Dial(p.Addr, tlsCfg)
		if err != nil {
			return nil, err
		}

		a.closers = append(a.closers, client.Close)
		panels = append(panels, panelsync.Panel{Addr: p.Addr, Conn: client})
	}

	a.panels = panels

	return a.panels, nil
}

// statsClient connects once to the sing-box v2ray_api, which listens on loopback only without TLS.
func (a *App) statsClient() (v2raystats.StatsServiceClient, error) {
	if a.stats != nil {
		return a.stats, nil
	}

	stats, closeStats, err := singboxctl.DialStats(a.cfg.StatsAddr)
	if err != nil {
		return nil, err
	}

	a.closers = append(a.closers, closeStats)
	a.stats = stats

	return a.stats, nil
}
