package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/artemlavr/giocondo/internal/admin"
	"github.com/artemlavr/giocondo/internal/store"
)

// runProvision installs the agent on a node from the command line:
//
//	panel provision ssh -node N -host H [-port 22] [-user root] [-identity FILE] [-password-stdin]
//	                    [-host-key SHA256:…] [-no-firewall] [-keep-key]
//	panel provision cloud-init -node N > user-data.yaml
//
// The panel address for the agent and the apt repository come from the panel's env (PANEL_AGENT_ADDR or
// PANEL_PUBLIC_URL, PANEL_APT_REPO). A new node certificate is issued.
func runProvision(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: panel provision ssh|cloud-init -node NAME [flags]")
	}
	fs := flag.NewFlagSet("panel provision "+args[0], flag.ContinueOnError)
	db := fs.String("db", envOr("PANEL_DB", defaultDB()), "panel database")
	pkiDir := fs.String("pki", envOr("PANEL_PKI_DIR", defaultPKIDir()), "panel PKI directory")
	nodeName := fs.String("node", "", "node name")
	host := fs.String("host", "", "server address (ssh; default: node IP or host)")
	port := fs.Int("port", 22, "ssh port")
	user := fs.String("user", "root", "ssh user (not root: commands run with sudo -n)")
	identity := fs.String("identity", "", "private key file (in addition to the panel key)")
	passwordStdin := fs.Bool("password-stdin", false, "read the ssh password from the first line of stdin")
	hostKey := fs.String("host-key", "", "expected host key fingerprint SHA256:… (default: trust on first use)")
	noFirewall := fs.Bool("no-firewall", false, "do not enable ufw")
	keepKey := fs.Bool("keep-key", false, "keep the panel key in authorized_keys")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *nodeName == "" {
		return errors.New("-node is required")
	}
	ctx := context.Background()
	owners := newOwnerKeeper(*db, *pkiDir)
	defer owners.restore()
	st, err := store.Open(ctx, *db)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	svc := &admin.Service{Store: st, PKIDir: *pkiDir, AptRepo: os.Getenv("PANEL_APT_REPO"),
		AgentAddr: agentAddr(strings.TrimRight(os.Getenv("PANEL_PUBLIC_URL"), "/"))}
	if svc.AgentAddr == "" {
		return errors.New("panel address for agents is unknown: set PANEL_AGENT_ADDR or PANEL_PUBLIC_URL")
	}
	n, err := findNodeByName(ctx, st, *nodeName)
	if err != nil {
		return fmt.Errorf("node %q: %w", *nodeName, err)
	}

	switch args[0] {
	case "cloud-init":
		data, _, err := svc.CloudInit(ctx, n.ID)
		if err != nil {
			return err
		}
		_, err = stdout.Write(data)
		return err
	case "ssh":
		in := admin.SSHInput{Host: *host, Port: *port, User: *user, HostKeySHA256: *hostKey, NoFirewall: *noFirewall, KeepKey: *keepKey}
		if *identity != "" {
			data, err := os.ReadFile(*identity)
			if err != nil {
				return err
			}
			in.PrivateKey = string(data)
		}
		if *passwordStdin {
			line, err := bufio.NewReader(stdin).ReadString('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			in.Password = strings.TrimRight(line, "\r\n")
		}
		return svc.InstallSSH(ctx, n.ID, in, stdout)
	default:
		return fmt.Errorf("unknown command: provision %s", args[0])
	}
}
