package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The agent lives in the same repository as the panel but must reach the node without its code:
// no database, subscriptions, provisioning or bot. The test fails if such an import appears.
var forbiddenAgentDeps = []string{
	"github.com/artemlavr/giocondo/internal/store",
	"github.com/artemlavr/giocondo/internal/subscription",
	"github.com/artemlavr/giocondo/internal/provision",
	"github.com/artemlavr/giocondo/internal/dns",
	"github.com/artemlavr/giocondo/internal/bot",
	"modernc.org/sqlite",
}

func TestAgentDoesNotImportPanel(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go tool not in PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, goBin, "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, bad := range forbiddenAgentDeps {
			if dep == bad || strings.HasPrefix(dep, bad+"/") {
				t.Errorf("agent depends on %s: agent must not include panel code", dep)
			}
		}
	}
}
