package bench

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var packageRoles = []string{"panel", "agent", "sing-box"}

type debPackage struct {
	Path, Role, Version string
}

func selectPackages(source, arch string) ([]debPackage, error) {
	if arch != "arm64" && arch != "amd64" {
		return nil, fmt.Errorf("unsupported package architecture %q", arch)
	}
	f, err := os.Open(filepath.Join(source, "SHA256SUMS"))
	if err != nil {
		return nil, fmt.Errorf("read package checksums: %w", err)
	}
	defer func() { _ = f.Close() }()
	sums := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return nil, errors.New("malformed SHA256SUMS line")
		}
		name := strings.TrimPrefix(fields[1], "*")
		if _, exists := sums[name]; exists {
			return nil, fmt.Errorf("duplicate checksum for %s", name)
		}
		digest, err := hex.DecodeString(fields[0])
		if err != nil || len(digest) != sha256.Size {
			return nil, fmt.Errorf("invalid SHA256SUMS digest for %s", name)
		}
		sums[name] = strings.ToLower(fields[0])
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	var packages []debPackage
	version := ""
	for _, role := range packageRoles {
		matches, err := filepath.Glob(filepath.Join(source, "giocondo-vpn-"+role+"_*_"+arch+".deb"))
		if err != nil {
			return nil, err
		}
		if len(matches) != 1 {
			return nil, fmt.Errorf("expected exactly one %s/%s deb in %s; build with make deb", role, arch, source)
		}
		path := matches[0]
		name := filepath.Base(path)
		v := strings.TrimSuffix(strings.TrimPrefix(name, "giocondo-vpn-"+role+"_"), "_"+arch+".deb")
		if v == "" || strings.ContainsAny(v, "_/\\") {
			return nil, fmt.Errorf("invalid package version in %s", name)
		}
		if version != "" && version != v {
			return nil, fmt.Errorf("packages have different versions: %s and %s", version, v)
		}
		version = v
		want, ok := sums[name]
		if !ok {
			return nil, fmt.Errorf("missing checksum: %s", name)
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		_, err = io.Copy(h, file)
		closeErr := file.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if hex.EncodeToString(h.Sum(nil)) != want {
			return nil, fmt.Errorf("package checksum mismatch: %s", name)
		}
		packages = append(packages, debPackage{Path: path, Role: role, Version: v})
	}
	return packages, nil
}

func copyPackages(source, destination, arch string) error {
	packages, err := selectPackages(source, arch)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(destination), ".packages-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	for _, p := range packages {
		if err := copyFile(p.Path, filepath.Join(tmp, filepath.Base(p.Path))); err != nil {
			return err
		}
	}
	if err := copyFile(filepath.Join(source, "SHA256SUMS"), filepath.Join(tmp, "SHA256SUMS")); err != nil {
		return err
	}
	// Recheck the bytes copied so a concurrently rebuilt source cannot enter the bench.
	if _, err := selectPackages(tmp, arch); err != nil {
		return err
	}
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	return os.Rename(tmp, destination)
}

func copyFile(source, dest string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}

const githubRepo = "lavr-art/giocondo"

func PreparePackages(ctx context.Context, arch, mode string) error {
	root := filepath.Join("bin", "network-e2e")
	source := os.Getenv("CANDIDATE_DEBS")
	if source == "" {
		source = "dist"
	}
	if err := copyPackages(source, filepath.Join(root, "candidate"), arch); err != nil {
		return err
	}
	if mode != "upgrade" {
		return nil
	}
	return prepareBaseline(ctx, root, arch, os.Getenv("BASELINE_VERSION"), &http.Client{Timeout: 120 * time.Second})
}

func prepareBaseline(ctx context.Context, root, arch, tag string, client *http.Client) error {
	if tag == "" {
		data, err := releaseRequest(ctx, client, "https://api.github.com/repos/"+githubRepo+"/releases?per_page=100")
		if err != nil {
			return err
		}
		var releases []struct {
			Tag        string `json:"tag_name"`
			Draft      bool   `json:"draft"`
			Prerelease bool   `json:"prerelease"`
		}
		if err := json.Unmarshal(data, &releases); err != nil {
			return fmt.Errorf("decode GitHub releases: %w", err)
		}
		for _, r := range releases {
			if !r.Draft && !r.Prerelease {
				tag = r.Tag
				break
			}
		}
	}
	if !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(tag) {
		return fmt.Errorf("baseline must be a stable vX.Y.Z release, got %q", tag)
	}
	cache := filepath.Join(root, "downloads", tag)
	if err := os.MkdirAll(cache, 0755); err != nil {
		return err
	}
	names := []string{"SHA256SUMS"}
	for _, r := range packageRoles {
		names = append(names, "giocondo-vpn-"+r+"_"+tag[1:]+"_"+arch+".deb")
	}
	for _, name := range names {
		target := filepath.Join(cache, name)
		if _, err := os.Stat(target); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		data, err := releaseRequest(ctx, client, "https://github.com/"+githubRepo+"/releases/download/"+tag+"/"+name)
		if err != nil {
			return err
		}
		if err := writeAtomic(target, data, 0644); err != nil {
			return err
		}
	}
	if err := copyPackages(cache, filepath.Join(root, "baseline"), arch); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(root, "baseline-version"), []byte(tag[1:]), 0644)
}

func releaseRequest(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "giocondo-upgrade-tests")
	if token := os.Getenv("GH_TOKEN"); token != "" && req.URL.Host == "api.github.com" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", url, response.StatusCode)
	}
	return readBounded(response.Body, 512<<20)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func commandOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 5 * time.Second
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func UnpackPackages(ctx context.Context, root string) error {
	archOut, err := commandOutput(ctx, "dpkg", "--print-architecture")
	if err != nil {
		return err
	}
	arch := strings.TrimSpace(string(archOut))
	packages, err := selectPackages(filepath.Join(root, "candidate"), arch)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(root, ".unpack-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	for _, p := range packages {
		out, err := commandOutput(ctx, "dpkg-deb", "-f", p.Path, "Package", "Architecture", "Version")
		if err != nil {
			return err
		}
		if err := validatePackageMetadata(string(out), p, arch); err != nil {
			return err
		}
		extract := filepath.Join(tmp, p.Role)
		if _, err := commandOutput(ctx, "dpkg-deb", "-x", p.Path, extract); err != nil {
			return err
		}
		binary := "usr/bin/giocondo-vpn-" + p.Role
		if p.Role == "sing-box" {
			binary = "usr/lib/giocondo-vpn/sing-box"
		}
		if err := copyFile(filepath.Join(extract, binary), filepath.Join(tmp, p.Role+".new")); err != nil {
			return err
		}
	}
	// Validate and extract the whole release before replacing any working binary.
	for _, p := range packages {
		if err := os.Rename(filepath.Join(tmp, p.Role+".new"), filepath.Join(root, p.Role)); err != nil {
			return err
		}
	}
	return writeAtomic(filepath.Join(root, "candidate-version"), []byte(packages[0].Version), 0644)
}

func validatePackageMetadata(metadata string, p debPackage, arch string) error {
	values := make(map[string]string)
	for line := range strings.SplitSeq(metadata, "\n") {
		key, value, ok := strings.Cut(line, ": ")
		if ok {
			values[key] = strings.TrimSpace(value)
		}
	}
	for _, field := range []struct{ key, want string }{{"Package", "giocondo-vpn-" + p.Role}, {"Architecture", arch}, {"Version", p.Version}} {
		if values[field.key] != field.want {
			return fmt.Errorf("package %s %s mismatch: got %q, want %q", filepath.Base(p.Path), field.key, values[field.key], field.want)
		}
	}
	return nil
}
