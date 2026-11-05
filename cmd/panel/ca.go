package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/artemlavr/giocondo/internal/app/panelapp"
	"github.com/artemlavr/giocondo/internal/repository/pkifs"
	"github.com/artemlavr/giocondo/internal/service/pki"
)

const caUsage = `usage:
  panel ca init   -dir pki
  panel ca server -dir pki -host panel.example.com[,203.0.113.5]
  panel ca agent  -dir pki -node n1`

func runCA(ctx context.Context, args []string, _ io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New(caUsage)
	}

	fs := flag.NewFlagSet("panel ca "+args[0], flag.ContinueOnError)
	dir := pkiFlag(fs, "dir")
	hosts := fs.String("host", "", "server: comma-separated domains/IPs agents use to reach the panel")
	node := fs.String("node", "", "agent: node name (must match the node in the panel)")

	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	certs := pki.Certificates{Store: pkifs.Disk{Dir: *dir}}
	p := pkifs.Paths{Dir: *dir}

	switch args[0] {
	case "init":
		if err := certs.InitCA(ctx); err != nil {
			return err
		}

		_, err := fmt.Fprintf(stdout, "CA created: %s (keep %s secret)\n", p.CACert(), p.CAKey())

		return err
	case "server":
		if err := certs.IssueServer(ctx, panelapp.SplitList(*hosts)); err != nil {
			return err
		}

		_, err := fmt.Fprintf(stdout, "panel certificate: %s, %s\n", p.ServerCert(), p.ServerKey())

		return err
	case "agent":
		if *node == "" {
			return errors.New("-node is required")
		}

		if err := certs.IssueAgent(ctx, *node); err != nil {
			return err
		}

		_, err := fmt.Fprintf(stdout, "agent certificate for %s: %s, %s\ncopy them with %s to the node\n",
			*node, p.AgentCert(*node), p.AgentKey(*node), p.CACert())

		return err
	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], caUsage)
	}
}
