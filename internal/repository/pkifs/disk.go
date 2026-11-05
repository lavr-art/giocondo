package pkifs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Disk persists PEM certificate and private key material in the PKI directory.
type Disk struct{ Dir string }

// LoadCA reads the CA certificate and key.
func (d Disk) LoadCA(ctx context.Context) ([]byte, []byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	p := Paths(d)

	cert, err := os.ReadFile(p.CACert())
	if err != nil {
		return nil, nil, err
	}

	key, err := os.ReadFile(p.CAKey())

	return cert, key, err
}

// CreateCA refuses to replace a CA whose private key already exists.
func (d Disk) CreateCA(ctx context.Context, cert, key []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	p := Paths(d)
	if _, err := os.Stat(p.CAKey()); err == nil {
		return fmt.Errorf("%s already exists", p.CAKey())
	}

	if err := os.MkdirAll(d.Dir, 0o700); err != nil {
		return err
	}

	// Exclusive creation also prevents concurrent initializations overwriting the CA key.
	f, err := os.OpenFile(p.CAKey(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}

	_, writeErr := f.Write(key)
	closeErr := f.Close()

	if writeErr != nil {
		return writeErr
	}

	if closeErr != nil {
		return closeErr
	}

	// Public certificates are intentionally world-readable: agents and clients fetch them over TLS.
	return os.WriteFile(p.CACert(), cert, 0o644) //nolint:gosec // public CA certificate, not a secret.
}

// WriteServer writes a server certificate and key.
func (d Disk) WriteServer(ctx context.Context, cert, key []byte) error {
	p := Paths(d)

	return writePair(ctx, p.ServerCert(), p.ServerKey(), cert, key)
}

// WriteAgent writes an agent certificate and key.
func (d Disk) WriteAgent(ctx context.Context, identity string, cert, key []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if !nodeNameRe.MatchString(identity) {
		return fmt.Errorf("bad node name %q", identity)
	}

	if err := os.MkdirAll(filepath.Join(d.Dir, "agents"), 0o700); err != nil {
		return err
	}

	p := Paths(d)

	return writePair(ctx, p.AgentCert(identity), p.AgentKey(identity), cert, key)
}

func writePair(ctx context.Context, certPath, keyPath string, cert, key []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		return err
	}
	// Public certificates are intentionally world-readable so clients can verify the node.
	return os.WriteFile(certPath, cert, 0o644) //nolint:gosec // public certificate, not a secret.
}
