package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/artemlavr/giocondo/internal/app/panelapp"
	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/platform/localagent"
	"github.com/artemlavr/giocondo/internal/repository/pkifs"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
	"github.com/artemlavr/giocondo/internal/repository/sqlite/nodes"
	"github.com/artemlavr/giocondo/internal/service/provisioning"
)

// runLocalNode turns the panel machine into a node (Runtime local): creates the node if missing, issues
// an agent certificate and installs it for the local giocondo-vpn-agent, which connects to 127.0.0.1.
// Run as root (sudo giocondo-vpn-panel local-node -host vpn.example.com): it writes /etc/giocondo-vpn/agent and
// restarts the agent. Database and PKI files touched by the command are returned to their owner,
// the panel user. -check and -ports only read the state and never migrate the database.
func runLocalNode(ctx context.Context, args []string, stdout io.Writer, rt provisioning.Runtime) error {
	fs := flag.NewFlagSet("panel local-node", flag.ContinueOnError)
	db := dbFlag(fs)
	pkiDir := pkiFlag(fs, "pki")
	name := fs.String("name", "local", "node name")
	host := fs.String("host", "", "domain or IP clients connect to (required for a new node)")
	protocols := fs.String("protocols", "vless,hysteria2,wireguard", "protocols for a new node")
	grpcListen := fs.String("grpc", envOr("PANEL_GRPC_LISTEN", panelapp.DefaultGRPCListen), "panel gRPC listen address (PANEL_GRPC_LISTEN)")
	restart := fs.Bool("restart", true, "enable and restart giocondo-vpn-agent via systemctl (false — e.g. in a container)")
	check := fs.Bool("check", false, "wait for the existing local node to be ready (read-only)")
	ports := fs.Bool("ports", false, "print existing local node firewall ports (read-only)")
	agentCert := fs.String("agent-cert", "/etc/giocondo-vpn/agent/agent.crt", "installed agent certificate")
	config := fs.String("config", "/etc/giocondo-vpn/sing-box/config.json", "installed sing-box config")
	timeout := fs.Duration("timeout", 120*time.Second, "local node readiness timeout")

	if err := fs.Parse(args); err != nil {
		return err
	}

	switch {
	case *check:
		return checkLocalNode(ctx, *db, *agentCert, *config, *timeout, stdout)
	case *ports:
		return printLocalNodePorts(ctx, *db, *agentCert, *name, stdout)
	}

	if l, ok := rt.(*localagent.LocalRuntime); ok && !*restart {
		l.RestartCmd = []string{}
	}

	_, port, err := net.SplitHostPort(*grpcListen)
	if err != nil || *grpcListen == "off" {
		return fmt.Errorf("panel gRPC must listen on a port for the local agent, got %q", *grpcListen)
	}

	panelAddr := net.JoinHostPort("127.0.0.1", port)
	if ok, err := pkifs.ServerCovers(*pkiDir, "127.0.0.1"); err != nil {
		return fmt.Errorf("panel certificate: %w (create it with `panel ca server -dir %s -host <domain>,127.0.0.1`)", err, *pkiDir)
	} else if !ok {
		return fmt.Errorf("panel certificate does not cover 127.0.0.1: reissue it with `panel ca server -dir %s -host <domain>,127.0.0.1`", *pkiDir)
	}

	opened, err := openPanel(ctx, *db, panelapp.Config{PKIDir: *pkiDir})
	if err != nil {
		return err
	}
	defer opened.Close()

	node, err := opened.nodes.NodeByName(ctx, *name)

	switch {
	case errors.Is(err, panel.ErrNotFound):
		if node, err = createLocalNode(ctx, opened, *name, *host, *protocols); err != nil {
			return err
		}

		_, _ = fmt.Fprintf(stdout, "node %s created (runtime local)\n", node.Name)
	case err != nil:
		return err
	case node.Runtime != panel.RuntimeLocal:
		return fmt.Errorf("node %q exists and is not local (runtime %s)", node.Name, node.Runtime)
	}

	cert, _, err := opened.app.ProvisioningService().IssueAgentCert(ctx, node.ID)
	if err != nil {
		return err
	}

	cert.PanelAddr = panelAddr

	if err := rt.Install(ctx, *node, cert); err != nil {
		return err
	}

	_, err = fmt.Fprintf(stdout, "local agent for node %s installed, panel address %s\n", node.Name, panelAddr)

	return err
}

func createLocalNode(ctx context.Context, opened *panelState, name, host, protocols string) (*panel.Node, error) {
	if host == "" {
		return nil, fmt.Errorf("node %q does not exist yet: -host is required", name)
	}

	in := panel.CreateNodeInput{Name: name, Host: host, Runtime: panel.RuntimeLocal}

	for _, p := range panelapp.SplitList(protocols) {
		switch p {
		case "vless", "vless-reality":
			in.VLESS = &panel.VLESSInput{}
		case "hysteria2", "hy2":
			in.Hysteria2 = &panel.Hy2Input{}
		case "wireguard", "wg":
			in.WireGuard = &panel.WGNodeInput{}
		default:
			return nil, fmt.Errorf("unknown protocol %q", p)
		}
	}

	v, err := opened.app.NodeService().CreateNode(ctx, in)
	if err != nil {
		return nil, err
	}

	return opened.nodes.GetNode(ctx, v.ID)
}

// checkLocalNode waits until the agent named by its verified certificate runs the desired config.
func checkLocalNode(ctx context.Context, db, agentCert, config string, timeout time.Duration, stdout io.Writer) error {
	if timeout <= 0 {
		return errors.New("timeout must be positive")
	}

	// Heartbeats recorded before the command started may predate the agent restart.
	since := time.Now().UTC().Truncate(time.Second)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	name, err := localagent.VerifiedAgentName(agentCert)
	if err != nil {
		return err
	}

	st, err := sqlite.OpenReadOnly(ctx, db)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	if err := localagent.WaitReady(ctx, nodes.New(st), name, config, since); err != nil {
		return err
	}

	_, err = fmt.Fprintf(stdout, "local node %s ready\n", name)

	return err
}

// printLocalNodePorts prints the local node ports for the firewall. The installed agent certificate,
// when present, names the node instead of -name.
func printLocalNodePorts(ctx context.Context, db, agentCert, name string, stdout io.Writer) error {
	if _, err := os.Stat(agentCert); err == nil {
		if name, err = localagent.AgentName(agentCert); err != nil {
			return err
		}
	}

	st, err := sqlite.OpenReadOnly(ctx, db)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	ports, err := localagent.FirewallPorts(ctx, nodes.New(st), name)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(stdout, strings.Join(ports, " "))

	return err
}
