//go:build e2e

package networke2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

type object = map[string]any

type bench struct {
	t          *testing.T
	root, work string
	report     []string
	nodeID     int
	install    string
	pollError  error
	ctx        context.Context
}
type commandResult struct {
	stdout, stderr string
	err            error
}
type terminalError struct{ message string }

func (e terminalError) Error() string { return e.message }

func newBench(t *testing.T) *bench {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate network bench source")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := ""
	for _, start := range []string{cwd, filepath.Dir(source)} {
		for candidate := start; ; candidate = filepath.Dir(candidate) {
			if _, err := os.Stat(filepath.Join(candidate, "go.mod")); err == nil {
				if _, err := os.Stat(filepath.Join(candidate, "test", "network-e2e", "compose.yaml")); err == nil {
					root = candidate
					break
				}
			}
			if filepath.Dir(candidate) == candidate {
				break
			}
		}
		if root != "" {
			break
		}
	}
	if root == "" {
		t.Fatal("cannot find repository root")
	}
	b := &bench{t: t, root: root, work: filepath.Join(root, "bin", "network-e2e"), install: installerCommand}
	b.ctx = t.Context()
	if deadline, ok := t.Deadline(); ok {
		// Leave time for bounded firewall cleanup and failure diagnostics.
		reserve := min(3*time.Minute, time.Until(deadline)/10)
		ctx, cancel := context.WithDeadline(b.ctx, deadline.Add(-reserve))
		b.ctx = ctx
		t.Cleanup(cancel)
	}
	t.Cleanup(func() {
		if t.Failed() {
			b.diagnostics()
		}
	})
	return b
}

var secretPattern = regexp.MustCompile(`(?i)(vpc_[a-z0-9_-]+|Bearer\s+[^\s"']+|/sub/[^\s?"']+|-----BEGIN[^-]*PRIVATE KEY-----[\s\S]*?-----END[^-]*PRIVATE KEY-----|"(?:password|private_key|preshared_key|token|uuid)"\s*:\s*"[^"]*"|(?:PANEL_ADMIN_PASSWORD|AGENT_[A-Z_]*(?:KEY|TOKEN)|password|private_key|preshared_key|token)\s*[=:]\s*[^\s,}]+)`)

func redact(s string) string { return secretPattern.ReplaceAllString(s, "[REDACTED]") }
func (b *bench) command(ctx context.Context, timeout time.Duration, input string, program string, args ...string) commandResult {
	if ctx == b.t.Context() {
		ctx = b.ctx
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return commandResult{stdout.String(), stderr.String(), err}
}
func (b *bench) composeWith(ctx context.Context, timeout time.Duration, args ...string) commandResult {
	return b.command(ctx, timeout, "", "docker", append([]string{"compose", "-f", filepath.Join(b.root, "test", "network-e2e", "compose.yaml")}, args...)...)
}
func (b *bench) compose(args ...string) commandResult {
	return b.composeWith(b.t.Context(), 3*time.Minute, args...)
}
func (b *bench) execute(service string, args ...string) commandResult {
	return b.composeWith(b.t.Context(), 2*time.Minute, append([]string{"exec", "-T", service}, b.containerArgs(2*time.Minute, args)...)...)
}

func (b *bench) containerArgs(limit time.Duration, args []string) []string {
	if deadline, ok := b.ctx.Deadline(); ok {
		limit = min(limit, time.Until(deadline))
	}
	// Stop container processes before the Docker client loses its connection.
	limit = max(time.Millisecond, limit-3*time.Second)
	return append([]string{"timeout", "--signal=TERM", "--kill-after=2s", fmt.Sprintf("%.3fs", limit.Seconds())}, args...)
}

// A successful shell prints the tool's status separately from Compose's status.
// Negative assertions must distinguish a rejected connection from a broken fixture.
func (b *bench) executeStatus(service string, args ...string) (commandResult, int, error) {
	const marker = "\n__NETWORK_E2E_COMMAND_EXIT__="
	script := `"$@"; status=$?; printf '\n__NETWORK_E2E_COMMAND_EXIT__=%s\n' "$status"`
	r := b.execute(service, append([]string{"sh", "-c", script, "network-e2e"}, args...)...)
	if err := commandError(r); err != nil {
		return r, -1, err
	}
	index := strings.LastIndex(r.stdout, marker)
	if index < 0 {
		return r, -1, fmt.Errorf("container command returned no exit-status marker")
	}
	status, err := strconv.Atoi(strings.TrimSpace(r.stdout[index+len(marker):]))
	if err != nil || status < 0 || status > 255 {
		return r, -1, fmt.Errorf("container command returned an invalid exit-status marker")
	}
	r.stdout = r.stdout[:index]
	return r, status, nil
}
func (b *bench) shell(command string) commandResult {
	args := append([]string{"exec", "-T", "installer"}, b.containerArgs(10*time.Minute, []string{"sh", "-c", command})...)
	return b.composeWith(b.t.Context(), 10*time.Minute, args...)
}
func commandError(r commandResult) error {
	if r.err == nil {
		return nil
	}
	return fmt.Errorf("command failed: %w: %s\n%s", r.err, redact(r.stdout), redact(r.stderr))
}
func (b *bench) checked(r commandResult) string {
	b.t.Helper()
	if err := commandError(r); err != nil {
		b.t.Fatal(err)
	}
	return r.stdout
}
func (b *bench) require(ok bool, format string, args ...any) {
	b.t.Helper()
	if !ok {
		b.t.Fatal(redact(fmt.Sprintf(format, args...)))
	}
}
func (b *bench) equal(got, want any, label string) {
	b.t.Helper()
	// Config and identity comparisons may contain private keys and credentials.
	b.require(reflect.DeepEqual(got, want), "%s differs", label)
}
func (b *bench) pass(label string) {
	b.t.Helper()
	b.t.Log("PASS", label)
	b.report = append(b.report, label)
}
func (b *bench) pause(d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-b.ctx.Done():
		return b.ctx.Err()
	case <-timer.C:
		return nil
	}
}
func (b *bench) wait(label string, timeout time.Duration, check func() (bool, error)) {
	b.t.Helper()
	parent := b.ctx
	ctx, cancel := context.WithTimeout(parent, timeout)
	b.ctx = ctx
	defer func() { b.ctx = parent; cancel() }()
	deadline, _ := ctx.Deadline()
	var lastErr error
	for time.Now().Before(deadline) {
		b.pollError = nil
		ok, err := check()
		if err == nil && !ok {
			err = b.pollError
		}
		if err != nil {
			var terminal terminalError
			if errors.As(err, &terminal) {
				b.t.Fatal(redact(err.Error()))
			}
			lastErr = err
		} else if ok {
			b.pass(label)
			return
		}
		if err := b.pause(2 * time.Second); err != nil {
			break
		}
	}
	b.t.Fatalf("%s: timeout; last error: %s", label, redact(fmt.Sprint(lastErr)))
}
func decode(r commandResult) (any, error) {
	if err := commandError(r); err != nil {
		return nil, err
	}
	var v any
	err := json.Unmarshal([]byte(r.stdout), &v)
	return v, err
}
func (b *bench) api(method, path string, body any) (any, error) {
	args := []string{"/work/bench", "request", method, path}
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		args = append(args, string(data))
	}
	return decode(b.execute("panel", args...))
}
func (b *bench) mustAPI(method, path string, body any) any {
	b.t.Helper()
	v, err := b.api(method, path, body)
	if err != nil {
		b.t.Fatal(err)
	}
	return v
}
func obj(v any) object {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}
func list(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return nil
}
func str(v any) string { s, _ := v.(string); return s }
func (b *bench) intField(m object, key string) int {
	b.t.Helper()
	n, ok := m[key].(float64)
	b.require(ok && n == float64(int(n)), "required field %q must be an integral number", key)
	return int(n)
}
func (b *bench) boolField(m object, key string) bool {
	b.t.Helper()
	v, ok := m[key].(bool)
	b.require(ok, "required field %q must be a boolean", key)
	return v
}
func (b *bench) stringField(m object, key string) string {
	b.t.Helper()
	v, ok := m[key].(string)
	b.require(ok, "required field %q must be a string", key)
	return v
}
func (b *bench) node() (object, error) {
	v, e := b.api("GET", fmt.Sprintf("nodes/%d", b.nodeID), nil)
	return obj(v), e
}
func (b *bench) mustNode() object {
	b.t.Helper()
	n, e := b.node()
	if e != nil {
		b.t.Fatal(e)
	}
	return n
}
func (b *bench) nodePath(suffix string) string { return fmt.Sprintf("nodes/%d%s", b.nodeID, suffix) }
func (b *bench) job(v any) {
	op := obj(v)
	id := str(op["id"])
	b.require(id != "", "operation has no ID")
	b.wait("operation "+id, 5*time.Minute, func() (bool, error) {
		v, e := b.api("GET", "jobs/"+id, nil)
		if e != nil {
			return false, e
		}
		job := obj(v)
		if job["status"] == "failed" {
			return false, terminalError{fmt.Sprintf("operation failed: %v", job)}
		}
		return job["status"] == "ok", nil
	})
}
func (b *bench) write(name string, value []byte) {
	b.t.Helper()
	p := filepath.Join(b.work, name)
	if e := os.WriteFile(p, value, 0600); e != nil {
		b.t.Fatal(e)
	}
	if e := os.Chmod(p, 0600); e != nil {
		b.t.Fatal(e)
	}
}
func (b *bench) writeJSON(name string, value any) {
	data, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		b.t.Fatal(e)
	}
	b.write(name, append(data, '\n'))
}
func (b *bench) reportFile(name string) { b.write(name, []byte(strings.Join(b.report, "\n")+"\n")) }
func (b *bench) file(name string) string {
	b.t.Helper()
	data, e := os.ReadFile(filepath.Join(b.work, name))
	if e != nil {
		b.t.Fatal(e)
	}
	return string(data)
}
func (b *bench) fingerprints() map[string][32]byte {
	b.t.Helper()
	paths, e := filepath.Glob(filepath.Join(b.work, "client-*.json"))
	if e != nil {
		b.t.Fatal(e)
	}
	values := map[string][32]byte{}
	for _, p := range paths {
		data, e := os.ReadFile(p)
		if e != nil {
			b.t.Fatal(e)
		}
		values[filepath.Base(p)] = sha256.Sum256(data)
	}
	return values
}
func (b *bench) trafficStatus(port int, url string) (commandResult, int, error) {
	return b.executeStatus("client", "curl", "-fsS", "--max-time", "8", "--cacert", "/work/pki/ca.crt", "-x", fmt.Sprintf("http://127.0.0.1:%d", port), url)
}
func (b *bench) traffic(port int, url, challenge string) bool {
	r, status, err := b.trafficStatus(port, url)
	if err != nil {
		b.pollError = err
	} else if status != 0 {
		b.pollError = fmt.Errorf("client curl exited %d: %s", status, redact(r.stderr))
	} else if r.stdout != challenge {
		b.pollError = fmt.Errorf("client on port %d returned an unexpected challenge", port)
	}
	return err == nil && status == 0 && r.stdout == challenge
}

func (b *bench) requireBlockedClient(kind string) {
	b.t.Helper()
	port := map[string]int{"hy2": 19002, "wg": 19003}[kind]
	r, status, err := b.trafficStatus(port, "https://192.0.2.200:8443/health/challenge?challenge="+strings.Repeat("a", 64))
	if err != nil {
		b.t.Fatal("blocked-traffic fixture failed: ", err)
	}
	switch status {
	case 5, 6, 7, 28, 35, 52, 55, 56, 97:
		// Curl completed with a DNS, connection, timeout, TLS or proxy failure.
	default:
		b.t.Fatalf("blocked %s client did not report a curl connection failure (exit %d): %s", kind, status, redact(r.stderr))
	}
}
func (b *bench) clientTraffic(kind string) bool {
	port := map[string]int{"vless": 19001, "hy2": 19002, "wg": 19003}[kind]
	return b.traffic(port, "https://192.0.2.200:8443/health/challenge?challenge="+strings.Repeat("a", 64), strings.Repeat("a", 64))
}
func (b *bench) profile(url string) (object, error) {
	v, e := decode(b.execute("client", "/work/bench", "http", "GET", url))
	return obj(v), e
}
func (b *bench) startClient(name string, port int, out object, wg bool) {
	out["tag"] = "vpn"
	cfg := object{"log": object{"level": "error"}, "inbounds": []any{object{"type": "http", "listen": "127.0.0.1", "listen_port": port}}, "dns": object{"servers": []any{object{"type": "udp", "tag": "dns", "server": "172.30.77.3"}}, "strategy": "ipv4_only"}, "route": object{"final": "vpn", "default_domain_resolver": "dns"}}
	key := "outbounds"
	if wg {
		key = "endpoints"
	}
	cfg[key] = []any{out}
	b.writeJSON("client-"+name+".json", cfg)
	b.checked(b.execute("client", "sh", "-c", fmt.Sprintf("nohup /work/sing-box run -c /work/client-%s.json > /work/client-%s.log 2>&1 & echo $! > /work/client-%s.pid", name, name, name)))
}
func (b *bench) liveClients(names []string) {
	for _, name := range names {
		pid := strings.TrimSpace(b.checked(b.execute("client", "cat", "/work/client-"+name+".pid")))
		b.checked(b.execute("client", "kill", "-0", pid))
	}
}
func (b *bench) drop(service string, rule ...string) {
	b.t.Helper()
	args := append([]string{"iptables", "-t", "raw", "-I", "PREROUTING", "1"}, rule...)
	b.checked(b.execute(service, args...))
	b.t.Cleanup(func() {
		args := append([]string{"exec", "-T", service, "iptables", "-t", "raw", "-D", "PREROUTING"}, rule...)
		r := b.composeWith(context.Background(), 30*time.Second, args...)
		if err := commandError(r); err != nil {
			b.t.Error("remove temporary iptables rule:", err)
		}
	})
}
func sortedKeys(m map[string]object) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func (b *bench) diagnostics() {
	for _, service := range []string{"panel", "node1", "node2", "installer", "client"} {
		args := []string{"logs", "--tail", "100", service}
		if service == "node1" || service == "node2" || service == "installer" {
			args = []string{"exec", "-T", service, "journalctl", "--no-pager", "-n", "100", "-u", "giocondo-vpn-agent", "-u", "giocondo-vpn-panel", "-u", "giocondo-vpn-sing-box"}
		}
		r := b.composeWith(context.Background(), 20*time.Second, args...)
		b.t.Logf("diagnostics %s:\n%s\n%s", service, redact(r.stdout), redact(r.stderr))
	}
}
