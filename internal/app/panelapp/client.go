package panelapp

import (
	"context"
	"fmt"
	"maps"
	"net"
	"slices"

	"github.com/artemlavr/giocondo/internal/clients/cloudflare"
	"github.com/artemlavr/giocondo/internal/clients/dnsresolver"
	"github.com/artemlavr/giocondo/internal/clients/probepoint"
	"github.com/artemlavr/giocondo/internal/clients/ssh"
	"github.com/artemlavr/giocondo/internal/clients/webhook"
	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/probe"
	"github.com/artemlavr/giocondo/internal/platform/installation"
	"github.com/artemlavr/giocondo/internal/platform/protocheck"
	"github.com/artemlavr/giocondo/internal/repository/pkifs"
	"github.com/artemlavr/giocondo/internal/service/pki"
	"github.com/artemlavr/giocondo/internal/worker/events"
)

// withClients fills every port missing from d from the configuration. Nothing here performs I/O; a port
// whose settings are absent stays nil, which disables the facility.
func (a *App) withClients(d Dependencies) Dependencies {
	cfg := a.cfg

	if d.Artifacts == nil {
		d.Artifacts = installation.Artifacts{}
	}

	if cfg.PKIDir != "" {
		if d.Issuer == nil {
			d.Issuer = pki.Certificates{Store: pkifs.Disk{Dir: cfg.PKIDir}}
		}

		if d.Remote == nil {
			d.Remote = ssh.Remote{PKIDir: cfg.PKIDir}
		}
	}

	if d.Checker == nil && cfg.SingboxBinary != "" {
		d.Checker = &protocheck.Local{Binary: cfg.SingboxBinary, ChallengeURL: cfg.ChallengeURL, RootCAFile: cfg.ProbeCA}
	}

	// The external probe point is added by Run; a private copy keeps the caller's map unchanged.
	points := make(map[string]probe.Checker, len(d.ProbePoints)+1)
	maps.Copy(points, d.ProbePoints)
	d.ProbePoints = points

	// Real resolution verifies only the real DNS provider; a supplied DNS port brings its own ResolveA.
	if d.DNS == nil && cfg.DNSDomain != "" {
		client := &cloudflare.Cloudflare{ZoneID: cfg.DNSZoneID, Token: cfg.DNSToken, BaseURL: cfg.DNSAPIURL}
		d.DNS = cloudflare.DNS{Manager: client}

		if d.ResolveA == nil {
			d.ResolveA = resolveA(cfg.DNSVerifyServer)
		}
	}

	if d.Notify == nil && len(cfg.WebhookURLs) > 0 {
		a.hooks = events.New(cfg.WebhookURLs, webhook.New(cfg.WebhookSecret))
		d.Notify = func(e panel.Event) { a.hooks.Send(e) }
	}

	return d
}

// resolveA checks a new A record through the authoritative servers or through a configured resolver.
func resolveA(verifyServer string) func(context.Context, string, string) error {
	if verifyServer == "" {
		return dnsresolver.ResolveAuthoritative
	}

	return func(ctx context.Context, host, ip string) error {
		resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, verifyServer)
		}}

		ips, e := resolver.LookupHost(ctx, host)
		if e != nil {
			return e
		}

		if slices.Contains(ips, ip) {
			return nil
		}

		return fmt.Errorf("verification DNS does not return new IP")
	}
}

// addExternalProbe registers the remote check point; it reads the CA file, so it runs in Run before any
// service is built.
func (a *App) addExternalProbe() error {
	if a.cfg.ProbeURL == "" {
		return nil
	}

	client, err := probepoint.HTTPClient(a.cfg.ProbeCA)
	if err != nil {
		return err
	}

	a.deps.ProbePoints["external"] = &probepoint.Remote{URL: a.cfg.ProbeURL, Token: a.cfg.ProbeToken, Client: client}

	return nil
}
