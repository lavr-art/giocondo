package provision

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// KeyComment is the comment of the panel public key in authorized_keys; the key is removed by it.
const KeyComment = "vpnctl-panel"

// DeployKey is the panel SSH key for installing the agent on your own servers.
type DeployKey struct {
	Signer ssh.Signer
	// AuthorizedKey is the line for ~/.ssh/authorized_keys: "ssh-ed25519 AAAA… vpnctl-panel".
	AuthorizedKey string
}

// LoadOrCreateDeployKey reads dir/id_ed25519 or creates it (0600) on the first call.
func LoadOrCreateDeployKey(dir string) (*DeployKey, error) {
	path := filepath.Join(dir, "id_ed25519")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if data, err = createKey(dir, path); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " " + KeyComment
	return &DeployKey{Signer: signer, AuthorizedKey: line}, nil
}

func createKey(dir, path string) ([]byte, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, KeyComment)
	if err != nil {
		return nil, err
	}
	data := pem.EncodeToMemory(block)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// O_EXCL: two concurrent first calls do not overwrite each other's key.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return nil, err
	}
	return data, f.Close()
}
