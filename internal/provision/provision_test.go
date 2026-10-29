package provision

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestDeployKeyCreatedOnce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ssh")
	k1, err := LoadOrCreateDeployKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(k1.AuthorizedKey, "ssh-ed25519 ") || !strings.HasSuffix(k1.AuthorizedKey, " "+KeyComment) {
		t.Errorf("authorized key %q", k1.AuthorizedKey)
	}
	st, _ := os.Stat(filepath.Join(dir, "id_ed25519"))
	if st.Mode().Perm() != 0o600 {
		t.Errorf("key mode %o", st.Mode().Perm())
	}
	k2, err := LoadOrCreateDeployKey(dir)
	if err != nil || k2.AuthorizedKey != k1.AuthorizedKey {
		t.Errorf("second load: %v %q", err, k2.AuthorizedKey)
	}
}

var testFiles = []File{
	{"ca.crt", 0o644, []byte("CA")},
	{"agent.key", 0o600, []byte("KEY")},
	{"install-agent.sh", 0o755, []byte("#!/bin/sh\necho installed \"$@\"\n")},
}

func TestCloudInit(t *testing.T) {
	data, err := CloudInit(testFiles, "./install-agent.sh --panel 'p:9443'")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("#cloud-config\n")) {
		t.Fatalf("no #cloud-config header:\n%s", data)
	}
	var cc cloudConfig
	if err := yaml.Unmarshal(data, &cc); err != nil {
		t.Fatal(err)
	}
	if len(cc.WriteFiles) != 3 || cc.WriteFiles[1].Path != "/root/giocondo-vpn/agent.key" || cc.WriteFiles[1].Permissions != "0600" {
		t.Fatalf("write_files %+v", cc.WriteFiles)
	}
	if got, _ := base64.StdEncoding.DecodeString(cc.WriteFiles[1].Content); string(got) != "KEY" {
		t.Errorf("content %q", got)
	}
	if len(cc.RunCmd) != 1 || !strings.Contains(cc.RunCmd[0][2], "./install-agent.sh --panel 'p:9443'") ||
		!strings.Contains(cc.RunCmd[0][2], "rm -rf /root/giocondo-vpn") {
		t.Errorf("runcmd %q", cc.RunCmd)
	}
}

func TestTarGz(t *testing.T) {
	data, err := TarGz("n1", testFiles, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	gz, _ := gzip.NewReader(bytes.NewReader(data))
	tr := tar.NewReader(gz)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	if strings.Join(names, ",") != "n1/ca.crt,n1/agent.key,n1/install-agent.sh" {
		t.Errorf("names %v", names)
	}
}

// The scripts run in a real sh with a temporary HOME.
func TestScripts(t *testing.T) {
	home := t.TempDir()
	sh := func(script string, stdin []byte) string {
		t.Helper()
		cmd := exec.Command("sh", "-c", script)
		cmd.Env = append(os.Environ(), "HOME="+home, "TMPDIR="+home)
		cmd.Stdin = bytes.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return string(out)
	}
	bundle, _ := TarGz("n1", testFiles, time.Now())
	out := sh(InstallScript("n1", "./install-agent.sh --panel 'p:1'"), bundle)
	if !strings.Contains(out, "installed --panel p:1") {
		t.Errorf("install output %q", out)
	}
	if m, _ := filepath.Glob("/tmp/giocondo-vpn-install.*"); len(m) > 0 {
		// mktemp uses /tmp explicitly: check that this run's directory is removed.
		for _, d := range m {
			if _, err := os.Stat(filepath.Join(d, "n1", "agent.key")); err == nil {
				t.Errorf("temporary dir %s left behind", d)
			}
		}
	}

	line := "ssh-ed25519 AAAAkey " + KeyComment
	keys := filepath.Join(home, ".ssh", "authorized_keys")
	sh(KeyScript(line, true), nil)
	sh(KeyScript(line, true), nil) // the second run does not duplicate
	if data, _ := os.ReadFile(keys); string(data) != line+"\n" {
		t.Errorf("after keep: %q", data)
	}
	if err := os.WriteFile(keys, []byte("ssh-ed25519 AAAAmine me@laptop\n"+line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sh(KeyScript(line, false), nil)
	data, _ := os.ReadFile(keys)
	if string(data) != "ssh-ed25519 AAAAmine me@laptop\n" {
		t.Errorf("after remove: %q", data)
	}
	if st, _ := os.Stat(keys); st.Mode().Perm() != 0o600 {
		t.Errorf("mode changed: %o", st.Mode().Perm())
	}
	// Only the panel key: the file becomes empty and the script does not fail.
	_ = os.WriteFile(keys, []byte(line+"\n"), 0o600)
	sh(KeyScript(line, false), nil)
	if data, _ := os.ReadFile(keys); len(data) != 0 {
		t.Errorf("only panel key: %q", data)
	}
}

func TestJobs(t *testing.T) {
	js := &Jobs{}
	j := js.Start(context.Background(), 7, "ssh", func(_ context.Context, log *Log) error {
		_, _ = io.WriteString(log, "step 1\n")
		return nil
	})
	if j.Status != JobRunning || j.NodeID != 7 {
		t.Fatalf("start %+v", j)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		v, ok := js.Get(j.ID)
		if !ok {
			t.Fatal("job lost")
		}
		if v.Status == JobOK {
			if v.Log != "step 1\n" || v.FinishedAt == nil {
				t.Errorf("finished %+v", v)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}

	big := js.Start(context.Background(), 1, "ssh", func(_ context.Context, log *Log) error {
		_, _ = log.Write(bytes.Repeat([]byte("x"), maxJobLog+100))
		return io.ErrUnexpectedEOF
	})
	time.Sleep(50 * time.Millisecond)
	v, _ := js.Get(big.ID)
	if v.Status != JobFailed || v.Error == "" || !strings.HasSuffix(v.Log, "log truncated\n") || len(v.Log) > maxJobLog+100 {
		t.Errorf("big job status %s err %q len %d", v.Status, v.Error, len(v.Log))
	}

	for range maxJobs + 5 {
		js.Start(context.Background(), 1, "ssh", func(context.Context, *Log) error { return nil })
	}
	if _, ok := js.Get(j.ID); ok {
		t.Error("old job was not evicted")
	}
}
