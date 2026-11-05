package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/artemlavr/giocondo/internal/app/panelapp"
	"github.com/artemlavr/giocondo/internal/service/tokens"
)

// runToken manages REST API tokens from the command line: the first admin token before the web UI is set up.
//
//	panel token create -name NAME -scope monitor|node-sync|admin
//	panel token list
//	panel token delete -id ID
func runToken(ctx context.Context, args []string, _ io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: panel token create|list|delete")
	}

	fs := flag.NewFlagSet("panel token "+args[0], flag.ContinueOnError)
	db := dbFlag(fs)
	name := fs.String("name", "", "token name (create)")
	scope := fs.String("scope", tokens.ScopeAdmin, "monitor, node-sync or admin (create)")
	id := fs.Int64("id", 0, "token id (delete)")

	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	opened, err := openPanel(ctx, *db, panelapp.Config{})
	if err != nil {
		return err
	}
	defer opened.Close()

	svc := opened.app.TokenService()

	switch args[0] {
	case "create":
		t, secret, err := svc.CreateToken(ctx, *name, *scope)
		if err != nil {
			return err
		}

		_, err = fmt.Fprintf(stdout, "token %d (%s, %s) — save it now, it is shown only once:\n%s\n", t.ID, t.Name, t.Scope, secret)

		return err
	case "list":
		list, err := svc.ListTokens(ctx)
		if err != nil {
			return err
		}

		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "ID\tNAME\tSCOPE\tCREATED\tLAST USED")

		for _, t := range list {
			last := "-"
			if t.LastUsedAt != nil {
				last = t.LastUsedAt.Format("2006-01-02 15:04")
			}

			_, _ = fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", t.ID, t.Name, t.Scope, t.CreatedAt.Format("2006-01-02 15:04"), last)
		}

		return tw.Flush()
	case "delete":
		if *id <= 0 {
			return errors.New("-id is required")
		}

		return svc.DeleteToken(ctx, *id)
	default:
		return fmt.Errorf("unknown command: token %s", args[0])
	}
}
