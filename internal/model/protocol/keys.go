package protocol

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
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
	raw, err := ParseWGKey(privateKey)
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

// ParseWGKey decodes a WireGuard key: 32 bytes in standard base64.
func ParseWGKey(s string) ([]byte, error) {
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
