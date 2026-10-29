package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/artemlavr/giocondo/internal/nodeapi/pki"
)

const caUsage = `usage:
  panel ca init   -dir pki
  panel ca server -dir pki -host panel.example.com[,203.0.113.5]
  panel ca agent  -dir pki -node n1`

func runCA(args []string) error {
	if len(args) == 0 {
		return errors.New(caUsage)
	}
	fs := flag.NewFlagSet("panel ca "+args[0], flag.ContinueOnError)
	dir := fs.String("dir", envOr("PANEL_PKI_DIR", defaultPKIDir()), "PKI directory")
	hosts := fs.String("host", "", "server: comma-separated domains/IPs agents use to reach the panel")
	node := fs.String("node", "", "agent: node name (must match the node in the panel)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	p := pki.Paths{Dir: *dir}
	switch args[0] {
	case "init":
		if err := pki.InitCA(*dir); err != nil {
			return err
		}
		fmt.Printf("CA created: %s (keep %s secret)\n", p.CACert(), p.CAKey())
	case "server":
		var list []string
		for _, h := range strings.Split(*hosts, ",") {
			if h = strings.TrimSpace(h); h != "" {
				list = append(list, h)
			}
		}
		if err := pki.IssueServer(*dir, list); err != nil {
			return err
		}
		fmt.Printf("panel certificate: %s, %s\n", p.ServerCert(), p.ServerKey())
	case "agent":
		if *node == "" {
			return errors.New("-node is required")
		}
		if err := pki.IssueAgent(*dir, *node); err != nil {
			return err
		}
		fmt.Printf("agent certificate for %s: %s, %s\ncopy them with %s to the node\n",
			*node, p.AgentCert(*node), p.AgentKey(*node), p.CACert())
	default:
		fmt.Fprintln(os.Stderr, caUsage)
		return fmt.Errorf("unknown command %q", args[0])
	}
	return nil
}
