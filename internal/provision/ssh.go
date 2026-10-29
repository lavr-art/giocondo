package provision

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSHTarget is where and how to connect. The password and key are used only in memory.
type SSHTarget struct {
	Host       string
	Port       int    // 0 — 22
	User       string // "" — root
	Password   string
	PrivateKey []byte // PEM/OpenSSH; in addition to the panel key
	// HostKeySHA256 is the expected host key fingerprint ("SHA256:…"); empty means trust on first connection.
	HostKeySHA256 string
}

// InstallOptions is what to do on the server.
type InstallOptions struct {
	Bundle  []byte // tar.gz with directory Dir (TarGz)
	Dir     string // directory inside the archive that Cmd runs from
	Cmd     string // install command, e.g. ./install-agent.sh --panel …
	KeepKey bool   // keep the panel key in authorized_keys (removed by default)
}

// DialTimeout is how long to wait for TCP and the SSH handshake.
const DialTimeout = 20 * time.Second

// Install connects over SSH, unpacks Bundle into a temporary directory, runs Cmd
// (through sudo -n if the user is not root) and cleans up; output goes to log line by line.
// Then it removes the panel key from authorized_keys or, with KeepKey, adds it. It returns
// the server host key fingerprint.
func Install(ctx context.Context, t SSHTarget, key *DeployKey, opt InstallOptions, log io.Writer) (string, error) {
	// Session stdout and stderr are copied to log from different goroutines.
	log = &lockedWriter{w: log}
	client, fp, err := dial(ctx, t, key)
	if err != nil {
		return fp, err
	}
	defer func() { _ = client.Close() }()
	// Cancelling ctx closes the connection: commands on the server are interrupted.
	stop := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stop()
	_, _ = fmt.Fprintf(log, "connected to %s as %s, host key %s\n", net.JoinHostPort(t.Host, strconv.Itoa(port(t))), user(t), fp)

	uid, err := output(client, "id -u")
	if err != nil {
		return fp, fmt.Errorf("id -u: %w", err)
	}
	sudo := ""
	if strings.TrimSpace(uid) != "0" {
		sudo = "sudo -n "
		_, _ = fmt.Fprintln(log, "not root: using sudo -n")
	}

	if err := run(client, sudo+"sh -c "+quote(InstallScript(opt.Dir, opt.Cmd)), bytes.NewReader(opt.Bundle), log); err != nil {
		if ctx.Err() != nil {
			return fp, ctx.Err()
		}
		return fp, fmt.Errorf("install: %w", err)
	}
	if err := run(client, "sh -c "+quote(KeyScript(key.AuthorizedKey, opt.KeepKey)), nil, log); err != nil {
		return fp, fmt.Errorf("authorized_keys: %w", err)
	}
	return fp, nil
}

func port(t SSHTarget) int {
	if t.Port == 0 {
		return 22
	}
	return t.Port
}

func user(t SSHTarget) string {
	if t.User == "" {
		return "root"
	}
	return t.User
}

func dial(ctx context.Context, t SSHTarget, key *DeployKey) (*ssh.Client, string, error) {
	signers := []ssh.Signer{key.Signer}
	if len(t.PrivateKey) > 0 {
		s, err := ssh.ParsePrivateKey(t.PrivateKey)
		if err != nil {
			return nil, "", fmt.Errorf("private key: %w", err)
		}
		signers = append(signers, s)
	}
	auth := []ssh.AuthMethod{ssh.PublicKeys(signers...)}
	if t.Password != "" {
		auth = append(auth, ssh.Password(t.Password),
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = t.Password
				}
				return answers, nil
			}))
	}
	var fp string
	cfg := &ssh.ClientConfig{
		User: user(t),
		Auth: auth,
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			fp = ssh.FingerprintSHA256(k)
			if t.HostKeySHA256 != "" && fp != t.HostKeySHA256 {
				return fmt.Errorf("host key %s does not match expected %s", fp, t.HostKeySHA256)
			}
			return nil
		},
		Timeout: DialTimeout,
	}
	addr := net.JoinHostPort(t.Host, strconv.Itoa(port(t)))
	dctx, cancel := context.WithTimeout(ctx, DialTimeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(dctx, "tcp", addr)
	if err != nil {
		return nil, "", err
	}
	_ = conn.SetDeadline(time.Now().Add(DialTimeout))
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, fp, fmt.Errorf("ssh %s: %w", addr, err)
	}
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(c, chans, reqs), fp, nil
}

func output(c *ssh.Client, cmd string) (string, error) {
	s, err := c.NewSession()
	if err != nil {
		return "", err
	}
	defer func() { _ = s.Close() }()
	out, err := s.CombinedOutput(cmd)
	return string(out), err
}

func run(c *ssh.Client, cmd string, stdin io.Reader, log io.Writer) error {
	s, err := c.NewSession()
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	s.Stdin = stdin
	s.Stdout, s.Stderr = log, log
	err = s.Run(cmd)
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		return fmt.Errorf("exit status %d", exit.ExitStatus())
	}
	return err
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// InstallScript is an sh script: unpack the tar.gz from stdin into a temporary directory, run cmd
// from directory dir in it and remove everything afterwards.
func InstallScript(dir, cmd string) string {
	return `set -e
d=$(mktemp -d /tmp/giocondo-vpn-install.XXXXXX)
trap 'rm -rf "$d"' EXIT
tar xzf - -C "$d"
cd "$d"/` + quote(dir) + `
` + cmd + `
`
}

// KeyScript removes the panel key lines (KeyComment) from ~/.ssh/authorized_keys or, with
// keep, adds line if it is missing. File mode and owner are preserved.
func KeyScript(line string, keep bool) string {
	if keep {
		return `set -e
mkdir -p ~/.ssh && chmod 700 ~/.ssh
f=~/.ssh/authorized_keys
touch "$f" && chmod 600 "$f"
grep -qxF ` + quote(line) + ` "$f" || echo ` + quote(line) + ` >> "$f"
echo "panel key kept in authorized_keys"
`
	}
	return `f=~/.ssh/authorized_keys
[ -f "$f" ] || exit 0
grep -v ' ` + KeyComment + `$' "$f" > "$f.giocondo-vpn" || true
cat "$f.giocondo-vpn" > "$f" && rm -f "$f.giocondo-vpn"
echo "panel key removed from authorized_keys"
`
}

// quote quotes an sh argument in single quotes.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
