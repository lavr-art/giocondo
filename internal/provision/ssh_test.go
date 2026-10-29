package provision

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// fakeSSH is an in-process SSH server: it accepts exec, records commands and stdin, runs nothing.
type fakeSSH struct {
	addr     string
	hostKey  ssh.PublicKey
	password string
	authKey  ssh.PublicKey // allowed public key (nil means none)
	uid      string        // answer to `id -u`
	failCmd  string        // a command containing this substring exits with code 1

	mu    sync.Mutex
	cmds  []string
	stdin map[string][]byte
}

func newFakeSSH(t *testing.T) *fakeSSH {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	f := &fakeSSH{hostKey: signer.PublicKey(), uid: "0", stdin: map[string][]byte{}}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
			if f.password != "" && string(p) == f.password {
				return nil, nil
			}
			return nil, errors.New("bad password")
		},
		PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if f.authKey != nil && bytes.Equal(k.Marshal(), f.authKey.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("unknown key")
		},
	}
	cfg.AddHostKey(signer)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lis.Close() })
	f.addr = lis.Addr().String()
	go func() {
		for {
			c, err := lis.Accept()
			if err != nil {
				return
			}
			go f.serve(c, cfg)
		}
	}()
	return f
}

func (f *fakeSSH) serve(c net.Conn, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		_ = c.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		ch, creqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer func() { _ = ch.Close() }()
			for req := range creqs {
				if req.Type != "exec" {
					_ = req.Reply(false, nil)
					continue
				}
				n := binary.BigEndian.Uint32(req.Payload)
				cmd := string(req.Payload[4 : 4+n])
				_ = req.Reply(true, nil)
				in, _ := io.ReadAll(ch)
				f.mu.Lock()
				f.cmds = append(f.cmds, cmd)
				f.stdin[cmd] = in
				f.mu.Unlock()
				status := uint32(0)
				switch {
				case cmd == "id -u":
					_, _ = io.WriteString(ch, f.uid+"\n")
				case f.failCmd != "" && strings.Contains(cmd, f.failCmd):
					_, _ = io.WriteString(ch.Stderr(), "boom\n")
					status = 1
				default:
					_, _ = io.WriteString(ch, "ok: "+cmd[:min(len(cmd), 20)]+"\n")
				}
				_, _ = ch.SendRequest("exit-status", false, binary.BigEndian.AppendUint32(nil, status))
				return
			}
		}()
	}
}

func (f *fakeSSH) target() SSHTarget {
	host, port, _ := net.SplitHostPort(f.addr)
	p, _ := strconv.Atoi(port)
	return SSHTarget{Host: host, Port: p}
}

func testKey(t *testing.T) *DeployKey {
	t.Helper()
	k, err := LoadOrCreateDeployKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestInstallOverSSH(t *testing.T) {
	ctx := context.Background()
	f := newFakeSSH(t)
	f.password = "s3cret-pass"
	key := testKey(t)
	bundle, _ := TarGz("n1", testFiles, time.Now())
	opt := InstallOptions{Bundle: bundle, Dir: "n1", Cmd: "./install-agent.sh --panel 'p:9443'"}

	tg := f.target()
	tg.Password = f.password
	var log bytes.Buffer
	fp, err := Install(ctx, tg, key, opt, &log)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, log.String())
	}
	if fp != ssh.FingerprintSHA256(f.hostKey) || !strings.Contains(log.String(), fp) {
		t.Errorf("fingerprint %s, log:\n%s", fp, log.String())
	}
	if strings.Contains(log.String(), f.password) {
		t.Error("password leaked into the log")
	}
	f.mu.Lock()
	cmds := append([]string(nil), f.cmds...)
	f.mu.Unlock()
	if len(cmds) != 3 || cmds[0] != "id -u" || !strings.HasPrefix(cmds[1], "sh -c ") ||
		!strings.Contains(cmds[1], "install-agent.sh --panel") || !strings.Contains(cmds[2], KeyComment+"$") {
		t.Fatalf("commands %q", cmds)
	}
	if !bytes.Equal(f.stdin[cmds[1]], bundle) {
		t.Error("bundle was not sent to the install command")
	}

	// Not root: through sudo -n; KeepKey adds the panel key.
	f.uid = "1000"
	opt.KeepKey = true
	if _, err := Install(ctx, tg, key, opt, io.Discard); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	last := f.cmds[len(f.cmds)-2:]
	f.mu.Unlock()
	if !strings.HasPrefix(last[0], "sudo -n sh -c ") || !strings.Contains(last[1], ">> \"$f\"") {
		t.Errorf("non-root commands %q", last)
	}
}

func TestInstallAuthAndHostKey(t *testing.T) {
	ctx := context.Background()
	f := newFakeSSH(t)
	key := testKey(t)
	opt := InstallOptions{Dir: "n1", Cmd: "true"}

	// The panel key is already in authorized_keys: no password needed.
	f.authKey = key.Signer.PublicKey()
	if _, err := Install(ctx, f.target(), key, opt, io.Discard); err != nil {
		t.Fatalf("deploy key auth: %v", err)
	}
	// A wrong expected host key: refused before authentication.
	tg := f.target()
	tg.HostKeySHA256 = "SHA256:wrong"
	if _, err := Install(ctx, tg, key, opt, io.Discard); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("wrong host key: %v", err)
	}
	tg.HostKeySHA256 = ssh.FingerprintSHA256(f.hostKey)
	if _, err := Install(ctx, tg, key, opt, io.Discard); err != nil {
		t.Errorf("pinned host key: %v", err)
	}
	// Neither key nor password.
	f.authKey = nil
	if _, err := Install(ctx, f.target(), key, opt, io.Discard); err == nil {
		t.Error("auth without credentials succeeded")
	}
	// The installation failed: an error with the exit code, the key is left alone.
	f.authKey = key.Signer.PublicKey()
	f.failCmd = "tar xzf"
	var log bytes.Buffer
	if _, err := Install(ctx, f.target(), key, opt, &log); err == nil || !strings.Contains(err.Error(), "exit status 1") {
		t.Errorf("failed install: %v", err)
	}
	if !strings.Contains(log.String(), "boom") {
		t.Errorf("stderr not in log: %q", log.String())
	}
}
