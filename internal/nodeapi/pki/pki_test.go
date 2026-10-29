package pki

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func setup(t *testing.T) Paths {
	t.Helper()
	p := Paths{t.TempDir()}
	if err := InitCA(p.Dir); err != nil {
		t.Fatal(err)
	}
	if err := IssueServer(p.Dir, []string{"127.0.0.1", "panel.example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := IssueAgent(p.Dir, "n1"); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFilesAndPermissions(t *testing.T) {
	p := setup(t)
	for path, mode := range map[string]os.FileMode{
		p.CAKey(): 0o600, p.ServerKey(): 0o600, p.AgentKey("n1"): 0o600,
		p.CACert(): 0o644, p.ServerCert(): 0o644, p.AgentCert("n1"): 0o644,
	} {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != mode {
			t.Errorf("%s: mode %v, want %v", path, st.Mode().Perm(), mode)
		}
	}
	if err := InitCA(p.Dir); err == nil {
		t.Error("InitCA overwrote existing CA")
	}
	if err := IssueAgent(p.Dir, "../evil"); err == nil {
		t.Error("bad node name accepted")
	}
	if err := IssueAgent(t.TempDir(), "n2"); err == nil {
		t.Error("issued without CA")
	}
}

// serve starts a gRPC server with a health service and an interceptor that records the client CN.
func serve(t *testing.T, cfg *tls.Config) (string, <-chan string) {
	t.Helper()
	names := make(chan string, 1)
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(cfg)), grpc.UnaryInterceptor(
		func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
			n, err := NodeName(ctx)
			if err != nil {
				return nil, err
			}
			names <- n
			return h(ctx, req)
		}))
	healthpb.RegisterHealthServer(srv, health.NewServer())
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String(), names
}

func call(addr string, cfg *tls.Config) error {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(credentials.NewTLS(cfg)))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	return err
}

func TestMutualTLS(t *testing.T) {
	p := setup(t)
	srvCfg, err := ServerTLS(p.CACert(), p.ServerCert(), p.ServerKey())
	if err != nil {
		t.Fatal(err)
	}
	addr, names := serve(t, srvCfg)

	cliCfg, err := ClientTLS(p.CACert(), p.AgentCert("n1"), p.AgentKey("n1"), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := call(addr, cliCfg); err != nil {
		t.Fatalf("valid agent rejected: %v", err)
	}
	if got := <-names; got != "n1" {
		t.Errorf("node name %q", got)
	}

	// An agent with a certificate from a foreign CA.
	other := setup(t)
	bad, _ := ClientTLS(p.CACert(), other.AgentCert("n1"), other.AgentKey("n1"), "127.0.0.1")
	if err := call(addr, bad); err == nil {
		t.Error("agent from foreign CA accepted")
	}

	// The agent does not trust a panel with a certificate from a foreign CA.
	wrong, _ := ClientTLS(other.CACert(), other.AgentCert("n1"), other.AgentKey("n1"), "127.0.0.1")
	if err := call(addr, wrong); err == nil {
		t.Error("agent trusted foreign panel")
	}

	// The panel certificate cannot be used as a client one (no clientAuth).
	asClient, _ := ClientTLS(p.CACert(), p.ServerCert(), p.ServerKey(), "127.0.0.1")
	if err := call(addr, asClient); err == nil {
		t.Error("server certificate accepted as client certificate")
	}

	// Wrong panel name.
	misnamed, _ := ClientTLS(p.CACert(), p.AgentCert("n1"), p.AgentKey("n1"), "other.example.com")
	if err := call(addr, misnamed); err == nil {
		t.Error("wrong server name accepted")
	}
}

func TestNodeNameWithoutPeer(t *testing.T) {
	if _, err := NodeName(context.Background()); err == nil || errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestServerCovers(t *testing.T) {
	dir := t.TempDir()
	if err := InitCA(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := ServerCovers(dir, "127.0.0.1"); err == nil {
		t.Error("no server.crt: want error")
	}
	if err := IssueServer(dir, []string{"panel.example.com", "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]bool{"127.0.0.1": true, "panel.example.com": true, "10.0.0.1": false, "other.example.com": false} {
		if got, err := ServerCovers(dir, host); err != nil || got != want {
			t.Errorf("%s: %v %v, want %v", host, got, err, want)
		}
	}
}
