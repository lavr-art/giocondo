package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/artemlavr/giocondo/internal/app/panelapp"
	"github.com/artemlavr/giocondo/internal/platform/fileowner"
	"github.com/artemlavr/giocondo/internal/platform/localagent"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
	"github.com/artemlavr/giocondo/internal/repository/sqlite/nodes"
)

// command is one panel invocation: the server (empty name) or an operator command.
// The command-line syntax is a contract of install.sh, the deb scripts and the docs.
type command struct {
	name  string
	usage string
	run   func(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error
}

const serveUsage = "panel [-version]                           run the panel (HTTP + gRPC for agents)"

var commands = []command{
	{name: "ca", usage: "panel ca init|server|agent [flags]         manage the private CA for agent mTLS", run: runCA},
	{name: "token", usage: "panel token create|list|delete [flags]     manage REST API tokens", run: runToken},
	{name: "provision", usage: "panel provision ssh|cloud-init -node NAME  install the agent on a server", run: runProvision},
	{name: "local-node", usage: "panel local-node [-check|-ports] [flags]   make the panel machine a node (as root)", run: runLocalNodeCommand},
	{name: "check", usage: "panel check -pid PID                       wait for a running panel to become ready", run: runCheck},
	{name: "probe", usage: "panel probe -cert F -key F -allow LIST     run an external probe point", run: runProbe},
}

// lookup selects the command for the arguments; no arguments or leading flags run the server.
func lookup(args []string) (command, []string, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return command{usage: serveUsage, run: runServe}, args, nil
	}

	for _, c := range commands {
		if c.name == args[0] {
			return c, args[1:], nil
		}
	}

	return command{}, nil, fmt.Errorf("unknown command %q", args[0])
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "usage:\n  "+serveUsage)
	for _, c := range commands {
		_, _ = fmt.Fprintln(w, "  "+c.usage)
	}
}

func runLocalNodeCommand(ctx context.Context, args []string, _ io.Reader, stdout io.Writer) error {
	return runLocalNode(ctx, args, stdout, localagent.DefaultLocalRuntime())
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return def
}

func dbFlag(fs *flag.FlagSet) *string {
	return fs.String("db", envOr("PANEL_DB", panelapp.DefaultDB()), "panel database")
}

func pkiFlag(fs *flag.FlagSet, name string) *string {
	return fs.String(name, envOr("PANEL_PKI_DIR", panelapp.DefaultPKIDir()), "panel PKI directory")
}

// panelState is the panel database and application opened by an operator command.
type panelState struct {
	app     *panelapp.App
	store   *sqlite.DB
	nodes   *nodes.Repository
	restore func()
}

// openPanel opens (and migrates) the database and builds the application around it. Close also returns
// the database and PKI files to their owner when the command runs as root.
func openPanel(ctx context.Context, db string, cfg panelapp.Config) (*panelState, error) {
	restore := fileowner.Keep(db, cfg.PKIDir)

	st, err := sqlite.Open(ctx, db)
	if err != nil {
		restore()

		return nil, err
	}

	app := panelapp.New(cfg, panelapp.Dependencies{DB: st})

	return &panelState{app: app, store: st, nodes: nodes.New(st), restore: restore}, nil
}

func (p *panelState) Close() {
	_ = p.app.Close()
	_ = p.store.Close()

	p.restore()
}
