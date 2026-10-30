package bench

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveMovesOnlyRunArtifacts(t *testing.T) {
	root := t.TempDir()
	move := []string{"panel.db", "panel.db-wal", "records.json", "events.jsonl", "token-output", "results.txt", "client-1.json", "debug.log"}
	keep := []string{"bench", "candidate-version", "baseline-version", "control.log", "pki"}
	for _, name := range append(move, keep...) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Archive(root); err != nil {
		t.Fatal(err)
	}
	archives, err := os.ReadDir(filepath.Join(root, "archive"))
	if err != nil {
		t.Fatal(err)
	}
	if len(archives) != 1 {
		t.Fatalf("archive entries = %v", archives)
	}
	for _, name := range move {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("%s still present: %v", name, err)
		}
		data, err := os.ReadFile(filepath.Join(root, "archive", archives[0].Name(), name))
		if err != nil || string(data) != name {
			t.Fatalf("archived %s = %q, %v", name, data, err)
		}
	}
	for _, name := range keep {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("retained %s: %v", name, err)
		}
	}
}

func TestRunRejectsInvalidCommandArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"prepare-packages", "arm64"}, {"archive", "extra", "extra"}, {"request", "GET"}, {"http", "GET"}, {"control", "extra"}, {"unpack-packages", "extra"}, {"caddy-check", "extra"}} {
		var out bytes.Buffer
		err := Run(context.Background(), args, &out)
		if err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Fatalf("Run(%q) error = %v", args, err)
		}
		if out.Len() != 0 {
			t.Fatalf("Run(%q) output = %q", args, out.String())
		}
	}
}

func TestRunArchivesExplicitContainerRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "records.json"), []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"archive", root}, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "records.json")); !os.IsNotExist(err) {
		t.Fatalf("records not archived: %v", err)
	}
}
