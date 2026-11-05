package localagent

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/protocol"
)

// NodeStore reads the panel state of the local node.
type NodeStore interface {
	NodeByName(ctx context.Context, name string) (*panel.Node, error)
	ListInbounds(ctx context.Context, nodeID int64) ([]panel.Inbound, error)
}

const readinessPollInterval = 2 * time.Second

// AgentName returns the node name (certificate CN) of the installed agent certificate.
func AgentName(certPath string) (string, error) {
	cert, err := readAgentCert(certPath)
	if err != nil {
		return "", err
	}

	return certNodeName(cert)
}

// VerifiedAgentName is AgentName that also requires the certificate to match agent.key
// and to be a valid client certificate signed by ca.crt from the same directory.
func VerifiedAgentName(certPath string) (string, error) {
	cert, err := readAgentCert(certPath)
	if err != nil {
		return "", err
	}

	dir := filepath.Dir(certPath)
	if _, err := tls.LoadX509KeyPair(certPath, filepath.Join(dir, "agent.key")); err != nil {
		return "", errors.New("agent certificate and private key are invalid or do not match")
	}

	ca, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err != nil {
		return "", fmt.Errorf("agent CA: %w", err)
	}

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return "", errors.New("invalid agent CA")
	}

	opts := x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	if _, err := cert.Verify(opts); err != nil {
		return "", errors.New("agent certificate is expired or not signed by its CA")
	}

	return certNodeName(cert)
}

func readAgentCert(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("agent certificate: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("invalid agent certificate")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, errors.New("invalid agent certificate")
	}

	return cert, nil
}

func certNodeName(cert *x509.Certificate) (string, error) {
	if cert.Subject.CommonName == "" {
		return "", errors.New("agent certificate has no node name")
	}

	return cert.Subject.CommonName, nil
}

// FirewallPorts returns the inbound ports of the local node as "PORT/PROTO" for the firewall.
func FirewallPorts(ctx context.Context, nodes NodeStore, name string) ([]string, error) {
	node, err := nodes.NodeByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("local node %q: %w", name, err)
	}

	if node.Runtime != panel.RuntimeLocal {
		return nil, fmt.Errorf("node %q is not local", name)
	}

	inbounds, err := nodes.ListInbounds(ctx, node.ID)
	if err != nil {
		return nil, err
	}

	if len(inbounds) == 0 {
		return nil, errors.New("local node has no inbounds")
	}

	ports := make([]string, 0, len(inbounds))
	for _, in := range inbounds {
		if in.Spec.Port == 0 {
			return nil, errors.New("invalid inbound port")
		}

		proto := "udp"
		if in.Spec.Type == protocol.TypeVLESSReality {
			proto = "tcp"
		}

		ports = append(ports, fmt.Sprintf("%d/%s", in.Spec.Port, proto))
	}

	return ports, nil
}

// WaitReady waits until the local node reports a heartbeat after since and runs the desired
// config, and the installed config at configPath matches it. It only reads state.
func WaitReady(ctx context.Context, nodes NodeStore, name, configPath string, since time.Time) error {
	for {
		last := nodeReady(ctx, nodes, name, configPath, since)
		if last == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("local node readiness timed out: %w", last)
		case <-time.After(readinessPollInterval):
		}
	}
}

func nodeReady(ctx context.Context, nodes NodeStore, name, configPath string, since time.Time) error {
	node, err := nodes.NodeByName(ctx, name)
	if err != nil {
		return fmt.Errorf("read local node: %w", err)
	}

	if node.Runtime != panel.RuntimeLocal {
		return errors.New("agent node is not local")
	}

	if node.Status != panel.NodeOnline {
		return errors.New("agent is not online")
	}

	if node.LastSeen == nil || node.LastSeen.Before(since) {
		return errors.New("waiting for a fresh agent heartbeat")
	}

	if node.ApplyError != "" {
		return errors.New("agent reports a configuration apply error; inspect its journal")
	}

	if node.DesiredHash == "" || node.DesiredHash != node.AppliedHash {
		return errors.New("agent has not applied the desired configuration")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read installed config: %w", err)
	}

	if !json.Valid(data) {
		return errors.New("installed config is invalid JSON")
	}

	if fmt.Sprintf("%x", sha256.Sum256(data)) != node.DesiredHash {
		return errors.New("installed config does not match the desired configuration")
	}

	return nil
}
