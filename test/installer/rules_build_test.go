package installer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ruleSets are the sets deploy/rules/build.sh publishes, with what their decompiled sources contain in the tests.
var ruleSets = map[string]string{
	"sing-box/rule-set-geosite/geosite-ru-blocked":               `{"rules":[{"domain":["exact.example"],"domain_suffix":["blocked.example",".dotted.example"]}]}`,
	"sing-box/rule-set-geoip/geoip-ru-blocked":                   `{"rules":[{"ip_cidr":["192.0.2.0/24"]}]}`,
	"sing-box/rule-set-geosite/geosite-category-ru":              `{"rules":[{"domain_suffix":["category.example"]}]}`,
	"sing-box/rule-set-geosite/geosite-ru-available-only-inside": `{"rules":[{"domain_suffix":["inside.example"]}]}`,
	"sing-box/rule-set-geoip/geoip-ru":                           `{"rules":[{"ip_cidr":["198.51.100.0/24"]}]}`,
}

// runRulesBuild runs the script against local sources and stub tools: decompile copies the fixture and
// convert-ruleset writes its behavior and input, so the test sees what the script passed to Mihomo.
func runRulesBuild(t *testing.T, sets map[string]string) (string, string, error) {
	t.Helper()

	dir := t.TempDir()
	for path, body := range sets {
		file := filepath.Join(dir, "src", filepath.FromSlash(path)+".srs")
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	singBox := filepath.Join(dir, "sing-box")
	writeExec(t, singBox, `#!/bin/sh
[ "$1 $2 $3" = "rule-set decompile -o" ] || exit 64
cp "$5" "$4"
`)

	mihomo := filepath.Join(dir, "mihomo")
	writeExec(t, mihomo, `#!/bin/sh
[ "$1 $3" = "convert-ruleset text" ] || exit 64
{ echo "behavior=$2"; cat "$4"; } > "$5"
`)

	out := filepath.Join(dir, "out")
	cmd := exec.CommandContext(t.Context(), "sh", "../../deploy/rules/build.sh", out)
	cmd.Env = append(os.Environ(), "SING_BOX="+singBox, "MIHOMO="+mihomo, "RULES_BASE=file://"+filepath.ToSlash(filepath.Join(dir, "src")))
	output, err := cmd.CombinedOutput()

	return out, string(output), err
}

func writeExec(t *testing.T, path, body string) {
	t.Helper()

	//nolint:gosec // the stub tool has to be executable.
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestRulesBuildConvertsEverySet(t *testing.T) {
	out, output, err := runRulesBuild(t, ruleSets)
	if err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}

	want := map[string]string{
		"geosite-ru-blocked.mrs":               "behavior=domain\nexact.example\n+.blocked.example\n+.dotted.example\n",
		"geoip-ru-blocked.mrs":                 "behavior=ipcidr\n192.0.2.0/24\n",
		"geosite-category-ru.mrs":              "behavior=domain\n+.category.example\n",
		"geosite-ru-available-only-inside.mrs": "behavior=domain\n+.inside.example\n",
		"geoip-ru.mrs":                         "behavior=ipcidr\n198.51.100.0/24\n",
	}

	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != len(want) {
		t.Fatalf("%d files in the output, want %d", len(entries), len(want))
	}

	for name, body := range want {
		got, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}

		if string(got) != body {
			t.Errorf("%s = %q, want %q", name, got, body)
		}
	}
}

// A rejected set must fail the whole build: publishing part of the lists, or lists that silently lost
// rules, would change routing for every client.
func TestRulesBuildRejectsWhatItCannotConvert(t *testing.T) {
	const blocked = "sing-box/rule-set-geosite/geosite-ru-blocked"

	tests := []struct {
		name, set, body, want string
	}{
		{"keyword rule", blocked, `{"rules":[{"domain_keyword":["x"]}]}`, "domain_keyword"},
		{"regex rule", blocked, `{"rules":[{"domain_regex":["x"]}]}`, "domain_regex"},
		{"IP rule in a domain set", blocked, `{"rules":[{"ip_cidr":["192.0.2.0/24"]}]}`, "ip_cidr"},
		{"domain rule in an IP set", "sing-box/rule-set-geoip/geoip-ru", `{"rules":[{"domain":["x.example"]}]}`, "domain"},
		{"empty set", blocked, `{"rules":[]}`, "no rules"},
		{"malformed set", blocked, `{"rules":`, "geosite-ru-blocked"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sets := map[string]string{}
			for path, body := range ruleSets {
				sets[path] = body
			}

			sets[tt.set] = tt.body

			out, output, err := runRulesBuild(t, sets)
			if err == nil {
				t.Fatalf("build succeeded:\n%s", output)
			}

			if !strings.Contains(output, tt.want) {
				t.Errorf("output lacks %q:\n%s", tt.want, output)
			}

			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Errorf("a failed build left the output directory: %v", err)
			}
		})
	}
}

func TestRulesBuildFailsOnAMissingSource(t *testing.T) {
	sets := map[string]string{}

	for path, body := range ruleSets {
		if !strings.Contains(path, "geoip-ru-blocked") {
			sets[path] = body
		}
	}

	out, output, err := runRulesBuild(t, sets)
	if err == nil {
		t.Fatal(fmt.Errorf("build succeeded:\n%s", output))
	}

	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("a failed build left the output directory: %v", err)
	}
}
