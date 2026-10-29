package provision

import (
	"bytes"
	"context"
	"errors"
	"golang.org/x/crypto/ssh"
	"io"
	"strings"
)

func StopServices(ctx context.Context, t SSHTarget, key *DeployKey) error {
	c, _, err := dial(ctx, t, key)
	if err != nil {
		return err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	return runPrivileged(c, "systemctl stop vpnctl-agent.service vpnctl-sing-box.service")
}
func ExportACME(ctx context.Context, t SSHTarget, key *DeployKey) ([]byte, error) {
	c, _, err := dial(ctx, t, key)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	session, err := c.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	var buf limitedBuffer
	session.Stdout = &buf
	session.Stderr = io.Discard
	uid, e := output(c, "id -u")
	if e != nil {
		return nil, e
	}
	prefix := ""
	if strings.TrimSpace(uid) != "0" {
		prefix = "sudo -n "
	}
	err = session.Run(prefix + "tar czf - -C /var/lib/vpnctl/sing-box/acme .")
	if err != nil {
		return nil, errors.New("ACME state export failed")
	}
	return buf.Bytes(), nil
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 16<<20 {
		return 0, errors.New("ACME archive too large")
	}
	return b.Buffer.Write(p)
}

func StartServices(ctx context.Context, t SSHTarget, key *DeployKey) error {
	c, _, err := dial(ctx, t, key)
	if err != nil {
		return err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	return runPrivileged(c, "systemctl start vpnctl-sing-box.service vpnctl-agent.service")
}

func runPrivileged(c *ssh.Client, cmd string) error {
	uid, err := output(c, "id -u")
	if err != nil {
		return err
	}
	if strings.TrimSpace(uid) != "0" {
		cmd = "sudo -n " + cmd
	}
	return run(c, cmd, nil, io.Discard)
}
