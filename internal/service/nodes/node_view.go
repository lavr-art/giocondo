package nodes

import (
	"context"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

func (s *Reader) nodeView(ctx context.Context, n panel.Node) (panel.NodeView, error) {
	ins, err := s.nodes.ListInbounds(ctx, n.ID)
	if err != nil {
		return panel.NodeView{}, err
	}

	v := panel.NodeView{ID: n.ID, Name: n.Name, Host: n.Host, IP: n.IP, Region: n.Region, Status: string(n.Status),
		Runtime: string(n.Runtime), Dirty: n.Dirty(), ApplyError: n.ApplyError,
		LastSeen: n.LastSeen, CreatedAt: n.CreatedAt, Inbounds: []panel.InboundView{}}
	for _, in := range ins {
		v.Inbounds = append(v.Inbounds, panel.InboundView{Tag: in.Spec.Tag, Type: in.Spec.Type, Port: in.Spec.Port})
	}

	if v.Hosts, err = s.hosts.ListHosts(ctx, n.ID); err != nil {
		return panel.NodeView{}, err
	}

	v.ManagedDNS = n.ManagedDNS
	v.Warning = n.DownNotified
	v.AgentConnected = n.LastSeen != nil && s.hooks.Now().Sub(*n.LastSeen) < 2*time.Minute && n.Status == panel.NodeOnline

	if v.Addresses, err = s.network.ListAddresses(ctx, n.ID); err != nil {
		return panel.NodeView{}, err
	}

	if v.Probes, err = s.probes.ListProbes(ctx, n.ID); err != nil {
		return panel.NodeView{}, err
	}

	if v.Operations, err = s.network.ListNetworkOperations(ctx, n.ID); err != nil {
		return panel.NodeView{}, err
	}

	if preview, e := s.PreviewDNS(ctx, n.ID); e == nil {
		v.DNSPreview = &preview
	}

	return v, nil
}
