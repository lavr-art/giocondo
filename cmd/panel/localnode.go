package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/artemlavr/giocondo/internal/admin"
	"github.com/artemlavr/giocondo/internal/nodeapi/pki"
	"github.com/artemlavr/giocondo/internal/store"
)

// runLocalNode turns the panel machine into a node (Runtime local): creates the node if missing, issues
// an agent certificate and installs it for the local vpnctl-agent, which connects to 127.0.0.1.
// Run as root (sudo vpnctl-panel local-node -host vpn.example.com): it writes /etc/vpnctl/agent and
// restarts the agent. Database and PKI files touched by the command are returned to their owner,
// the panel user.
func runLocalNode(args []string, stdout io.Writer, rt admin.Runtime) error {
	fs := flag.NewFlagSet("panel local-node", flag.ContinueOnError)
	db := fs.String("db", envOr("PANEL_DB", defaultDB()), "panel database")
	pkiDir := fs.String("pki", envOr("PANEL_PKI_DIR", defaultPKIDir()), "panel PKI directory")
	name := fs.String("name", "local", "node name")
	host := fs.String("host", "", "domain or IP clients connect to (required for a new node)")
	protocols := fs.String("protocols", "vless,hysteria2,wireguard", "protocols for a new node")
	grpcListen := fs.String("grpc", envOr("PANEL_GRPC_LISTEN", ":9443"), "panel gRPC listen address (PANEL_GRPC_LISTEN)")
	restart := fs.Bool("restart", true, "enable and restart vpnctl-agent via systemctl (false — e.g. in a container)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if l, ok := rt.(*admin.LocalRuntime); ok && !*restart {
		l.RestartCmd = []string{}
	}
	_, port, err := net.SplitHostPort(*grpcListen)
	if err != nil || *grpcListen == "off" {
		return fmt.Errorf("panel gRPC must listen on a port for the local agent, got %q", *grpcListen)
	}
	panelAddr := net.JoinHostPort("127.0.0.1", port)
	if ok, err := pki.ServerCovers(*pkiDir, "127.0.0.1"); err != nil {
		return fmt.Errorf("panel certificate: %w (create it with `panel ca server -dir %s -host <domain>,127.0.0.1`)", err, *pkiDir)
	} else if !ok {
		return fmt.Errorf("panel certificate does not cover 127.0.0.1: reissue it with `panel ca server -dir %s -host <domain>,127.0.0.1`", *pkiDir)
	}

	owners := newOwnerKeeper(*db, *pkiDir)
	defer owners.restore()

	ctx := context.Background()
	st, err := store.Open(ctx, *db)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	svc := &admin.Service{Store: st, PKIDir: *pkiDir}

	node, err := findNodeByName(ctx, st, *name)
	switch {
	case errors.Is(err, store.ErrNotFound):
		if *host == "" {
			return fmt.Errorf("node %q does not exist yet: -host is required", *name)
		}
		in := admin.CreateNodeInput{Name: *name, Host: *host, Runtime: store.RuntimeLocal}
		for _, p := range strings.Split(*protocols, ",") {
			switch strings.TrimSpace(p) {
			case "vless", "vless-reality":
				in.VLESS = &admin.VLESSInput{}
			case "hysteria2", "hy2":
				in.Hysteria2 = &admin.Hy2Input{}
			case "wireguard", "wg":
				in.WireGuard = &admin.WGNodeInput{}
			case "":
			default:
				return fmt.Errorf("unknown protocol %q", p)
			}
		}
		v, err := svc.CreateNode(ctx, in)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stdout, "node %s created (runtime local)\n", v.Name)
		if node, err = st.GetNode(ctx, v.ID); err != nil {
			return err
		}
	case err != nil:
		return err
	case node.Runtime != store.RuntimeLocal:
		return fmt.Errorf("node %q exists and is not local (runtime %s)", node.Name, node.Runtime)
	}

	cert, _, err := svc.IssueAgentCert(ctx, node.ID)
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

func findNodeByName(ctx context.Context, st store.Store, name string) (*store.Node, error) {
	nodes, err := st.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if n.Name == name {
			return &n, nil
		}
	}
	return nil, store.ErrNotFound
}

// ownerKeeper remembers the owner of the database and the PKI directory and, after running as root,
// restores it on all database files (-wal, -shm) and PKI: otherwise the panel, running as its own user,
// could not write them.
type ownerKeeper struct {
	uid, gid int
	db, pki  string
	ok       bool
}

func newOwnerKeeper(db, pkiDir string) *ownerKeeper {
	k := &ownerKeeper{db: db, pki: pkiDir}
	if os.Geteuid() != 0 {
		return k
	}
	for _, p := range []string{db, pkiDir, filepath.Dir(db)} {
		if fi, err := os.Stat(p); err == nil {
			if st, ok := fi.Sys().(*syscall.Stat_t); ok {
				k.uid, k.gid, k.ok = int(st.Uid), int(st.Gid), true
				break
			}
		}
	}
	return k
}

func (k *ownerKeeper) restore() {
	if !k.ok || k.uid == 0 {
		return
	}
	for _, p := range []string{k.db, k.db + "-wal", k.db + "-shm"} {
		_ = os.Lchown(p, k.uid, k.gid)
	}
	if k.pki == "" {
		return
	}
	_ = filepath.WalkDir(k.pki, func(p string, _ fs.DirEntry, err error) error {
		if err == nil {
			_ = os.Lchown(p, k.uid, k.gid)
		}
		return nil
	})
}
