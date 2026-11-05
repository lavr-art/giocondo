// Package pki owns certificate issuance policy independently of persistence.
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
	"regexp"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

const (
	caValidity   = 10 * 365 * 24 * time.Hour
	leafValidity = 5 * 365 * 24 * time.Hour
)

var nodeNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// Store persists PEM material. CreateCA must refuse to replace an existing CA.
type Store interface {
	LoadCA(context.Context) (cert, key []byte, err error)
	CreateCA(ctx context.Context, cert, key []byte) error
	WriteServer(ctx context.Context, cert, key []byte) error
	WriteAgent(ctx context.Context, identity string, cert, key []byte) error
}

// Certificates issues the panel and agent certificates using its private CA.
type Certificates struct{ Store Store }

// InitCA creates the CA. An existing CA is not overwritten: otherwise all issued certificates
// would stop passing verification.
func (c Certificates) InitCA(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	tmpl := &x509.Certificate{
		SerialNumber:          newSerial(),
		Subject:               pkix.Name{CommonName: "giocondo-vpn CA"},
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

	certPEM, keyPEM, err := encodePair(der, key)
	if err != nil {
		return err
	}

	return c.Store.CreateCA(ctx, certPEM, keyPEM)
}

// IssueServer issues the panel certificate for the domains and/or IPs in hosts.
func (c Certificates) IssueServer(ctx context.Context, hosts []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

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

	_, cert, key, err := c.issue(ctx, tmpl)
	if err != nil {
		return err
	}

	return c.Store.WriteServer(ctx, cert, key)
}

// IssueAgent issues and persists an agent certificate with its identity as the CN.
func (c Certificates) IssueAgent(ctx context.Context, identity string) error {
	_, err := c.Issue(ctx, identity)

	return err
}

// Issue creates a fresh agent key and certificate and returns its installation material.
func (c Certificates) Issue(ctx context.Context, identity string) (panel.AgentCert, error) {
	if err := ctx.Err(); err != nil {
		return panel.AgentCert{}, err
	}

	if !nodeNameRe.MatchString(identity) {
		return panel.AgentCert{}, fmt.Errorf("bad node name %q", identity)
	}

	tmpl := &x509.Certificate{
		Subject:     pkix.Name{CommonName: identity},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	ca, cert, key, err := c.issue(ctx, tmpl)
	if err != nil {
		return panel.AgentCert{}, err
	}

	if err := c.Store.WriteAgent(ctx, identity, cert, key); err != nil {
		return panel.AgentCert{}, err
	}

	return panel.AgentCert{CACert: string(ca), AgentCert: string(cert), AgentKey: string(key)}, nil
}

func (c Certificates) issue(ctx context.Context, tmpl *x509.Certificate) ([]byte, []byte, []byte, error) {
	caPEM, caKeyPEM, err := c.Store.LoadCA(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load CA (run `panel ca init` first): %w", err)
	}

	pair, err := tls.X509KeyPair(caPEM, caKeyPEM)
	if err != nil {
		return nil, nil, nil, err
	}

	caCert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, nil, nil, err
	}

	caKey, ok := pair.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, nil, nil, errors.New("CA key is not ECDSA")
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}

	tmpl.SerialNumber = newSerial()
	tmpl.NotBefore = time.Now().Add(-time.Hour)
	tmpl.NotAfter = time.Now().Add(leafValidity)
	tmpl.KeyUsage = x509.KeyUsageDigitalSignature

	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, nil, err
	}

	certPEM, keyPEM, err := encodePair(der, key)

	return caPEM, certPEM, keyPEM, err
}

func encodePair(der []byte, key *ecdsa.PrivateKey) ([]byte, []byte, error) {
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(
		&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER},
	), nil
}

func newSerial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		panic(err) // crypto/rand does not fail on Linux
	}

	return n
}
