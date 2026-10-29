// Command genconf builds a sing-box config.json from YAML describing a node and its users.
//
//	genconf fill  -in node.yaml                   fill in missing keys and certificates in the YAML
//	genconf build -in node.yaml -out config.json  build the config (-check: run sing-box check)
//	genconf info  -in node.yaml [-user name]      client data: Reality key, WG configs
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/artemlavr/giocondo/internal/singbox"
)

// File is the input YAML format.
type File struct {
	Node  singbox.Node   `yaml:"node"`
	Users []singbox.User `yaml:"users"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "genconf:", err)
		os.Exit(1)
	}
}

const usage = `usage:
  genconf fill  -in node.yaml [-out node.yaml]
  genconf build -in node.yaml -out config.json [-check] [-v2ray-api 127.0.0.1:10085] [-log-level warn]
  genconf info  -in node.yaml [-user name]`

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "fill":
		return runFill(args[1:], stderr)
	case "build":
		return runBuild(args[1:], stderr)
	case "info":
		return runInfo(args[1:], stdout, stderr)
	case "-h", "-help", "--help", "help":
		_, _ = fmt.Fprintln(stdout, usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], usage)
	}
}

func newFlags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("genconf "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func runFill(args []string, stderr io.Writer) error {
	fs := newFlags("fill", stderr)
	in := fs.String("in", "", "input YAML")
	out := fs.String("out", "", "output YAML (default: rewrite -in; comments are not preserved)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" {
		return errors.New("-in is required")
	}
	if *out == "" {
		*out = *in
	}
	f, err := readFile(*in)
	if err != nil {
		return err
	}
	if err := singbox.Fill(&f.Node, f.Users); err != nil {
		return err
	}
	if err := singbox.Validate(&f.Node, f.Users); err != nil {
		return fmt.Errorf("after fill: %w", err)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(f); err != nil {
		return err
	}
	data := buf.Bytes()
	// The YAML holds private keys: owner only.
	return writeAtomic(*out, data, 0o600)
}

func runBuild(args []string, stderr io.Writer) error {
	fs := newFlags("build", stderr)
	in := fs.String("in", "", "input YAML")
	out := fs.String("out", "config.json", "output sing-box config")
	check := fs.Bool("check", false, "run `sing-box check` on the result if sing-box is in PATH")
	var opts singbox.Options
	fs.StringVar(&opts.V2RayAPIListen, "v2ray-api", singbox.DefaultV2RayAPIListen, "v2ray_api listen address")
	fs.StringVar(&opts.LogLevel, "log-level", singbox.DefaultLogLevel, "sing-box log level")
	fs.StringVar(&opts.ACMEDataDir, "acme-dir", singbox.DefaultACMEDataDir, "ACME data directory on the node")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" {
		return errors.New("-in is required")
	}
	f, err := readFile(*in)
	if err != nil {
		return err
	}
	cfg, err := singbox.Build(&f.Node, f.Users, opts)
	if err != nil {
		return fmt.Errorf("%w\n(missing keys? run `genconf fill -in %s` first)", err, *in)
	}
	data, err := cfg.Marshal()
	if err != nil {
		return err
	}
	if err := writeAtomic(*out, data, 0o600); err != nil {
		return err
	}
	if !*check {
		return nil
	}
	bin, err := exec.LookPath("sing-box")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "sing-box not found in PATH, skipping check")
		return nil
	}
	return singboxCheck(bin, *out)
}

func runInfo(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("info", stderr)
	in := fs.String("in", "", "input YAML")
	user := fs.String("user", "", "print only this user")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" {
		return errors.New("-in is required")
	}
	f, err := readFile(*in)
	if err != nil {
		return err
	}
	if err := singbox.Validate(&f.Node, f.Users); err != nil {
		return err
	}
	n := &f.Node
	_, _ = fmt.Fprintf(stdout, "node %s (%s)\n", n.ID, n.Host)
	for _, ib := range n.Inbounds {
		switch ib.Type {
		case singbox.TypeVLESSReality:
			pub, err := singbox.RealityPublicKey(ib.Reality.PrivateKey)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(stdout, "  %s: port %d/tcp, public_key %s, short_id %v, sni %s, flow xtls-rprx-vision\n",
				ib.EffectiveTag(), ib.Port, pub, ib.Reality.ShortIDs, ib.Reality.ServerName)
		case singbox.TypeHysteria2:
			line := fmt.Sprintf("  %s: port %d/udp, tls %s", ib.EffectiveTag(), ib.Port, ib.Hysteria2.TLSMode())
			if ib.Hysteria2.TLSMode() == singbox.TLSSelfSigned {
				pin, err := singbox.CertSHA256(ib.Hysteria2.CertPEM)
				if err != nil {
					return err
				}
				line += ", pinSHA256 " + pin + " (client: insecure=1 or pin)"
			}
			if ib.Hysteria2.ObfsPassword != "" {
				line += ", obfs salamander"
			}
			_, _ = fmt.Fprintln(stdout, line+", password = user uuid")
		case singbox.TypeWireGuard:
			_, _ = fmt.Fprintf(stdout, "  %s: port %d/udp, subnet %s\n", ib.EffectiveTag(), ib.Port, ib.WireGuard.WGSubnet())
		}
	}
	found := *user == ""
	for _, u := range f.Users {
		if *user != "" && u.Name != *user {
			continue
		}
		found = true
		_, _ = fmt.Fprintf(stdout, "\nuser %s uuid %s\n", u.Name, u.UUID)
		if n.WireGuardInbound() != nil {
			wg, err := singbox.WireGuardClientConfig(n, u)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(stdout, "--- wireguard %s.conf ---\n%s", u.Name, wg)
		}
	}
	if !found {
		return fmt.Errorf("user %q not found", *user)
	}
	return nil
}

func readFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f File
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &f, nil
}

func singboxCheck(bin, path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "check", "-c", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sing-box check: %w\n%s", err, out)
	}
	return nil
}

// writeAtomic writes the file through a temporary one in the same directory and a rename.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // after rename the file is gone
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
