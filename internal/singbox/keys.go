package singbox

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"strconv"
	"time"
)

// Reality stores x25519 keys in unpadded base64 (like `sing-box generate reality-keypair`),
// WireGuard in standard base64.
var realityEnc = base64.RawURLEncoding

// NewRealityKeypair generates an x25519 pair for VLESS Reality.
func NewRealityKeypair() (privateKey, publicKey string, err error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return realityEnc.EncodeToString(k.Bytes()), realityEnc.EncodeToString(k.PublicKey().Bytes()), nil
}

// RealityPublicKey derives the Reality public key from the private one (the client needs it as pbk= in the link).
func RealityPublicKey(privateKey string) (string, error) {
	raw, err := realityEnc.DecodeString(privateKey)
	if err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	k, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return "", err
	}
	return realityEnc.EncodeToString(k.PublicKey().Bytes()), nil
}

// NewShortID generates a Reality short_id: 8 hex characters.
func NewShortID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// NewWGKeypair generates a WireGuard key pair.
func NewWGKeypair() (privateKey, publicKey string, err error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(k.Bytes()), base64.StdEncoding.EncodeToString(k.PublicKey().Bytes()), nil
}

// WGPublicKey derives the WireGuard public key from the private one.
func WGPublicKey(privateKey string) (string, error) {
	raw, err := parseWGKey(privateKey)
	if err != nil {
		return "", err
	}
	k, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(k.PublicKey().Bytes()), nil
}

// NewPresharedKey generates a WireGuard preshared key.
func NewPresharedKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

func parseWGKey(s string) ([]byte, error) {
	if s == "" {
		return nil, errors.New("empty key")
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes, got %d", len(raw))
	}
	return raw, nil
}

// NewUUID generates a version 4 UUID.
func NewUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}

// SelfSignedCert issues a self-signed ECDSA P-256 certificate valid for 10 years for host (domain or IP).
func SelfSignedCert(host string) (certPEM, keyPEM string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return "", "", err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", err
	}
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	return certPEM, keyPEM, nil
}

// CertSHA256 returns the certificate SHA-256 in hex. Hysteria2 clients pin the self-signed
// certificate by it (pinSHA256 in the link).
func CertSHA256(certPEM string) (string, error) {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("no certificate in PEM")
	}
	sum := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(sum[:]), nil
}

func wgPrefix(subnet string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(subnet)
	if err != nil {
		return netip.Prefix{}, err
	}
	if !p.Addr().Is4() || p.Bits() > 30 {
		return netip.Prefix{}, fmt.Errorf("%s: need an IPv4 subnet of /30 or larger", subnet)
	}
	return p.Masked(), nil
}

// serverWGAddr is the server address in the WireGuard subnet (the first after the network address).
func serverWGAddr(p netip.Prefix) netip.Addr {
	return p.Addr().Next()
}

// AllocateWGAddress allocates a free /32 address in the subnet. The network address, the server address (.1),
// broadcast and taken addresses are skipped.
func AllocateWGAddress(subnet string, taken []string) (string, error) {
	p, err := wgPrefix(subnet)
	if err != nil {
		return "", err
	}
	used := map[netip.Addr]bool{}
	for _, t := range taken {
		if a, err := netip.ParsePrefix(t); err == nil {
			used[a.Addr()] = true
		} else if a, err := netip.ParseAddr(t); err == nil {
			used[a] = true
		}
	}
	for a := serverWGAddr(p).Next(); p.Contains(a); a = a.Next() {
		if !p.Contains(a.Next()) {
			break // broadcast
		}
		if !used[a] {
			return netip.PrefixFrom(a, 32).String(), nil
		}
	}
	return "", fmt.Errorf("no free addresses left in %s", subnet)
}

// splitDest parses a Reality dest "host:port"; without a port 443 is used.
func splitDest(dest string) (string, uint16, error) {
	if dest == "" {
		return "", 0, errors.New("empty")
	}
	host, portStr, err := net.SplitHostPort(dest)
	if err != nil {
		return dest, 443, nil //nolint:nilerr // no port given
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil || port == 0 {
		return "", 0, fmt.Errorf("bad port %q", portStr)
	}
	return host, uint16(port), nil
}
