package bench

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func packageFixture(t *testing.T, versions ...string) string {
	t.Helper()
	root := t.TempDir()
	var sums strings.Builder
	for i, role := range packageRoles {
		version := "1.2.3"
		if len(versions) > i {
			version = versions[i]
		}
		name := fmt.Sprintf("giocondo-vpn-%s_%s_arm64.deb", role, version)
		data := []byte("real package fixture for " + role)
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(data), name)
	}
	if err := os.WriteFile(filepath.Join(root, "SHA256SUMS"), []byte(sums.String()), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestSelectPackagesRejectsInvalidArtifactSet(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, string)
		want   string
	}{
		{"missing", func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "giocondo-vpn-agent_1.2.3_arm64.deb")); err != nil {
				t.Fatal(err)
			}
		}, "exactly one agent/arm64"},
		{"duplicate", func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "giocondo-vpn-panel_1.2.4_arm64.deb"), []byte("another"), 0600); err != nil {
				t.Fatal(err)
			}
		}, "exactly one panel/arm64"},
		{"checksum", func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "giocondo-vpn-agent_1.2.3_arm64.deb"), []byte("corrupt"), 0600); err != nil {
				t.Fatal(err)
			}
		}, "checksum mismatch"},
		{"checksum absent", func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "SHA256SUMS"), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}, "missing checksum"},
		{"checksum duplicate", func(t *testing.T, root string) {
			path := filepath.Join(root, "SHA256SUMS")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(data, data...), 0600); err != nil {
				t.Fatal(err)
			}
		}, "duplicate checksum"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := packageFixture(t)
			tc.mutate(t, root)
			_, err := selectPackages(root, "arm64")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v; want %q", err, tc.want)
			}
		})
	}
}

func TestSelectPackagesRequiresMatchingVersionsAndArchitecture(t *testing.T) {
	root := packageFixture(t, "1.2.3", "1.2.4", "1.2.3")
	if _, err := selectPackages(root, "arm64"); err == nil || !strings.Contains(err.Error(), "different versions") {
		t.Fatalf("error = %v", err)
	}
	root = packageFixture(t)
	if _, err := selectPackages(root, "amd64"); err == nil || !strings.Contains(err.Error(), "exactly one panel/amd64") {
		t.Fatalf("error = %v", err)
	}
	packages, err := selectPackages(root, "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 3 || packages[0].Version != "1.2.3" {
		t.Fatalf("packages = %+v", packages)
	}
}

func TestCopyPackagesPreservesVerifiedDestinationOnFailure(t *testing.T) {
	source := packageFixture(t)
	dest := filepath.Join(t.TempDir(), "candidate")
	if err := copyPackages(source, dest, "arm64"); err != nil {
		t.Fatal(err)
	}
	name := "giocondo-vpn-panel_1.2.3_arm64.deb"
	before, err := os.ReadFile(filepath.Join(dest, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, name), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyPackages(source, dest, "arm64"); err == nil {
		t.Fatal("corrupt packages accepted")
	}
	after, err := os.ReadFile(filepath.Join(dest, name))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("verified destination changed after rejection")
	}
}

func TestPackageMetadataMustMatchVerifiedArtifact(t *testing.T) {
	p := debPackage{Path: "giocondo-vpn-agent_1.2.3_arm64.deb", Role: "agent", Version: "1.2.3"}
	valid := "Package: giocondo-vpn-agent\nArchitecture: arm64\nVersion: 1.2.3\n"
	if err := validatePackageMetadata(valid, p, "arm64"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ old, new, field string }{{"arm64", "amd64", "Architecture"}, {"1.2.3", "1.2.4", "Version"}, {"giocondo-vpn-agent", "giocondo-vpn-panel", "Package"}} {
		err := validatePackageMetadata(strings.Replace(valid, tc.old, tc.new, 1), p, "arm64")
		if err == nil || !strings.Contains(err.Error(), tc.field+" mismatch") {
			t.Fatalf("metadata error = %v", err)
		}
	}
}
