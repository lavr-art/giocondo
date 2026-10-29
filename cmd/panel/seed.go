package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/artemlavr/giocondo/internal/singbox"
	"github.com/artemlavr/giocondo/internal/store"
)

// seedFile is the genconf format: node + users.
type seedFile struct {
	Node  singbox.Node   `yaml:"node"`
	Users []singbox.User `yaml:"users"`
}

// runSeed creates a node (name = node.id), its protocols and users from genconf YAML.
// A node and users that already exist by name are left untouched. Used to bring up
// a test node by hand without the panel API.
func runSeed(args []string) error {
	fs := flag.NewFlagSet("panel seed", flag.ContinueOnError)
	in := fs.String("in", "", "YAML in genconf format (run `genconf fill` first)")
	db := fs.String("db", envOr("PANEL_DB", defaultDB()), "panel database")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" {
		return errors.New("-in is required")
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	var f seedFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return fmt.Errorf("%s: %w", *in, err)
	}
	if err := singbox.Validate(&f.Node, f.Users); err != nil {
		return fmt.Errorf("%s is incomplete (run `genconf fill -in %s`): %w", *in, *in, err)
	}

	ctx := context.Background()
	st, err := store.Open(ctx, *db)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	n := store.Node{Name: f.Node.ID, Host: f.Node.Host}
	switch err := st.CreateNode(ctx, &n); {
	case errors.Is(err, store.ErrConflict):
		fmt.Printf("node %s already exists, skipped\n", n.Name)
	case err != nil:
		return err
	default:
		for _, in := range f.Node.Inbounds {
			if err := st.CreateInbound(ctx, &store.Inbound{NodeID: n.ID, Spec: in}); err != nil {
				return fmt.Errorf("inbound %s: %w", in.EffectiveTag(), err)
			}
		}
		fmt.Printf("node %s (%s) created with %d inbounds\n", n.Name, n.Host, len(f.Node.Inbounds))
	}

	for _, u := range f.Users {
		su := store.User{Name: u.Name, UUID: u.UUID, Enabled: true, WG: u.WG}
		switch err := st.CreateUser(ctx, &su); {
		case errors.Is(err, store.ErrConflict):
			fmt.Printf("user %s already exists, skipped\n", u.Name)
		case err != nil:
			return fmt.Errorf("user %s: %w", u.Name, err)
		default:
			fmt.Printf("user %s: /sub/%s\n", su.Name, su.Token)
		}
	}
	return nil
}
