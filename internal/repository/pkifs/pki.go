// Package pkifs stores certificate material on disk and loads TLS configurations.
//
// Directory layout:
//
//	ca.crt, ca.key          private CA
//	server.crt, server.key  panel certificate (ExtKeyUsage serverAuth)
//	agents/<node>.crt/.key  agent certificates (clientAuth), CN = node name
package pkifs

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var nodeNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// Paths returns file paths in the PKI directory.
type Paths struct{ Dir string }

// CACert is the CA certificate, given to the panel and all agents.
func (p Paths) CACert() string { return filepath.Join(p.Dir, "ca.crt") }

// CAKey is the CA key, panel only.
func (p Paths) CAKey() string { return filepath.Join(p.Dir, "ca.key") }

// ServerCert is the panel certificate.
func (p Paths) ServerCert() string { return filepath.Join(p.Dir, "server.crt") }

// ServerKey is the panel key.
func (p Paths) ServerKey() string { return filepath.Join(p.Dir, "server.key") }

// AgentCert is the path to a node agent certificate.
func (p Paths) AgentCert(node string) string { return filepath.Join(p.Dir, "agents", node+".crt") }

// AgentKey is the path to a node agent key.
func (p Paths) AgentKey(node string) string { return filepath.Join(p.Dir, "agents", node+".key") }

func loadPool(caFile string) (*x509.CertPool, error) {
	data, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("%s: no certificates", caFile)
	}

	return pool, nil
}

// ServerTLS is the panel TLS: it requires a client certificate from our CA.
func ServerTLS(caFile, certFile, keyFile string) (*tls.Config, error) {
	pool, err := loadPool(caFile)
	if err != nil {
		return nil, err
	}

	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{pair},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}, nil
}

// ClientTLS is the agent TLS: it verifies the panel against our CA, not the system roots.
// serverName is the name from the panel certificate (domain or IP).
func ClientTLS(caFile, certFile, keyFile, serverName string) (*tls.Config, error) {
	pool, err := loadPool(caFile)
	if err != nil {
		return nil, err
	}

	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{pair},
		RootCAs:      pool,
		ServerName:   serverName,
	}, nil
}

// ServerCovers reports whether the panel certificate is valid for host (domain or IP).
func ServerCovers(dir, host string) (bool, error) {
	data, err := os.ReadFile(Paths{dir}.ServerCert())
	if err != nil {
		return false, err
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return false, errors.New("server.crt: no PEM data")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, err
	}

	return cert.VerifyHostname(host) == nil, nil
}
