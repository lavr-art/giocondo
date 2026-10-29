// Package pki issues certificates for mTLS between the panel and agents and builds TLS configs.
//
// Directory layout:
//
//	ca.crt, ca.key          private CA
//	server.crt, server.key  panel certificate (ExtKeyUsage serverAuth)
//	agents/<node>.crt/.key  agent certificates (clientAuth), CN = node name
package pki

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

const (
	caValidity   = 10 * 365 * 24 * time.Hour
	leafValidity = 5 * 365 * 24 * time.Hour
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

// InitCA creates the CA. An existing CA is not overwritten: otherwise all issued certificates
// would stop passing verification.
func InitCA(dir string) error {
	p := Paths{dir}
	if _, err := os.Stat(p.CAKey()); err == nil {
		return fmt.Errorf("%s already exists", p.CAKey())
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          newSerial(),
		Subject:               pkix.Name{CommonName: "vpnctl CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writePair(p.CACert(), p.CAKey(), der, key)
}

// IssueServer issues the panel certificate for the domains and/or IPs in hosts.
func IssueServer(dir string, hosts []string) error {
	if len(hosts) == 0 {
		return errors.New("at least one host is required")
	}
	tmpl := &x509.Certificate{
		Subject:     pkix.Name{CommonName: hosts[0]},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	p := Paths{dir}
	return issue(p, tmpl, p.ServerCert(), p.ServerKey())
}

// IssueAgent issues an agent certificate. CN is the node name the panel identifies the node by.
func IssueAgent(dir, node string) error {
	if !nodeNameRe.MatchString(node) {
		return fmt.Errorf("bad node name %q", node)
	}
	tmpl := &x509.Certificate{
		Subject:     pkix.Name{CommonName: node},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	p := Paths{dir}
	if err := os.MkdirAll(filepath.Join(dir, "agents"), 0o700); err != nil {
		return err
	}
	return issue(p, tmpl, p.AgentCert(node), p.AgentKey(node))
}

func issue(p Paths, tmpl *x509.Certificate, certPath, keyPath string) error {
	caCert, caKey, err := loadCA(p)
	if err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	tmpl.SerialNumber = newSerial()
	tmpl.NotBefore = time.Now().Add(-time.Hour)
	tmpl.NotAfter = time.Now().Add(leafValidity)
	tmpl.KeyUsage = x509.KeyUsageDigitalSignature
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return err
	}
	return writePair(certPath, keyPath, der, key)
}

func loadCA(p Paths) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	pair, err := tls.LoadX509KeyPair(p.CACert(), p.CAKey())
	if err != nil {
		return nil, nil, fmt.Errorf("load CA (run `panel ca init` first): %w", err)
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, nil, err
	}
	key, ok := pair.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, nil, errors.New("CA key is not ECDSA")
	}
	return cert, key, nil
}

func writePair(certPath, keyPath string, der []byte, key *ecdsa.PrivateKey) error {
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return err
	}
	return os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

func newSerial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		panic(err) // crypto/rand does not fail on Linux
	}
	return n
}

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

// NodeName returns the CN of the verified client certificate from the gRPC call context.
func NodeName(ctx context.Context) (string, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return "", errors.New("no peer")
	}
	info, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return "", errors.New("connection is not TLS")
	}
	chains := info.State.VerifiedChains
	if len(chains) == 0 || len(chains[0]) == 0 {
		return "", errors.New("no verified client certificate")
	}
	cn := chains[0][0].Subject.CommonName
	if !nodeNameRe.MatchString(cn) {
		return "", fmt.Errorf("bad node name in certificate: %q", cn)
	}
	return cn, nil
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
