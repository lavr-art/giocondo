package nodes

import (
	"context"
	"strings"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/protocol"
	"github.com/artemlavr/giocondo/internal/service/operation"
	"github.com/artemlavr/giocondo/internal/service/singbox"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// CreateNode registers a node with protocols. The agent then needs a certificate (AgentBundle).
func (s *Service) CreateNode(ctx context.Context, in panel.CreateNodeInput) (panel.NodeView, error) {
	in.Name, in.Host = strings.TrimSpace(in.Name), strings.TrimSpace(in.Host)
	if !singbox.ValidUserName(in.Name) {
		return panel.NodeView{}, operation.Invalid("node name %q: use latin letters, digits, '.', '_' or '-'", in.Name)
	}

	if in.Host == "" {
		return panel.NodeView{}, operation.Invalid("host is required (domain or IP clients connect to)")
	}

	spec := protocol.Node{ID: in.Name, Host: in.Host}
	if v := in.VLESS; v != nil {
		p := &protocol.RealityParams{Dest: v.Dest, ServerName: v.ServerName}
		if p.Dest == "" {
			p.Dest = "www.microsoft.com:443"
		}

		if p.ServerName == "" {
			p.ServerName, _, _ = strings.Cut(p.Dest, ":")
		}

		spec.Inbounds = append(
			spec.Inbounds,
			protocol.Inbound{Type: protocol.TypeVLESSReality, Tag: "vless", Port: orPort(v.Port, 443), Reality: p},
		)
	}

	if h := in.Hysteria2; h != nil {
		p := &protocol.Hysteria2Params{}
		if h.ACME {
			p.TLS, p.ACMEEmail = protocol.TLSACME, h.ACMEEmail
		}

		if h.Obfs {
			pw, err := protocol.NewPresharedKey()
			if err != nil {
				return panel.NodeView{}, err
			}

			p.ObfsPassword = strings.TrimRight(pw, "=")
		}

		spec.Inbounds = append(
			spec.Inbounds,
			protocol.Inbound{Type: protocol.TypeHysteria2, Tag: "hy2", Port: orPort(h.Port, 8443), Hysteria2: p},
		)
	}

	if w := in.WireGuard; w != nil {
		spec.Inbounds = append(spec.Inbounds, protocol.Inbound{Type: protocol.TypeWireGuard, Tag: "wg", Port: orPort(
			w.Port,
			51820,
		),
			WireGuard: &protocol.WireGuardParams{}})
	}

	if len(spec.Inbounds) == 0 {
		return panel.NodeView{}, operation.Invalid("enable at least one protocol")
	}

	if err := singbox.Fill(&spec, nil); err != nil {
		return panel.NodeView{}, err
	}

	if err := singbox.Validate(&spec, nil); err != nil {
		return panel.NodeView{}, operation.Invalid("%v", err)
	}

	n := panel.Node{Name: in.Name, Host: in.Host, IP: strings.TrimSpace(in.IP), Region: strings.TrimSpace(in.Region),
		Status: panel.NodeNew, Runtime: in.Runtime}

	inbounds := make([]panel.Inbound, len(spec.Inbounds))
	for i, ib := range spec.Inbounds {
		inbounds[i].Spec = ib
	}

	if err := s.nodes.CreateNodeWithInbounds(ctx, &n, inbounds); err != nil {
		return panel.NodeView{}, err
	}

	if err := s.credentials.EnsureWG(ctx); err != nil {
		logger.FromCtx(ctx).Error("issue wireguard keys", "err", err)
	}

	if s.monitoringEnabled {
		if _, err := s.credentials.EnsureMonitor(ctx, n.ID); err != nil {
			// CheckNodes retries preparation; the node aggregate is already committed.
			logger.FromCtx(ctx).Error("prepare node monitor", "node", n.Name, "err", err)
		}
	}

	logger.FromCtx(ctx).Info("node created", "node", n.Name, "host", n.Host)
	s.hooks.Kick() // ensureWG may have issued keys: other WG nodes need new peers

	return s.views.GetNode(ctx, n.ID)
}

func orPort(p, def uint16) uint16 {
	if p == 0 {
		return def
	}

	return p
}
