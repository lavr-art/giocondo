package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/nodeapi/pki"
	"github.com/artemlavr/giocondo/internal/store"
)

func TestLocalNodeCheck(t *testing.T) {
	for _, scenario := range []string{"ready", "stale", "empty hash", "wrong hash", "apply error", "remote", "offline", "missing node", "wrong config", "invalid json", "bad key", "wrong CA", "missing config"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			if err := pki.InitCA(dir); err != nil {
				t.Fatal(err)
			}
			if err := pki.IssueAgent(dir, "local"); err != nil {
				t.Fatal(err)
			}
			certDir := filepath.Join(dir, "credentials")
			if err := os.Mkdir(certDir, 0700); err != nil {
				t.Fatal(err)
			}
			for src, dst := range map[string]string{"ca.crt": "ca.crt", "agents/local.crt": "agent.crt", "agents/local.key": "agent.key"} {
				data, err := os.ReadFile(filepath.Join(dir, src))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(certDir, dst), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			dbPath, config := filepath.Join(dir, "panel.db"), filepath.Join(dir, "config.json")
			data := []byte("{\"log\":{\"level\":\"error\"}}\n")
			if scenario == "invalid json" {
				data = []byte("broken")
			}
			if err := os.WriteFile(config, data, 0600); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			st, err := store.Open(ctx, dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = st.Close() }()
			n := store.Node{Name: "local", Host: "vpn.example.com", Runtime: store.RuntimeLocal}
			if scenario == "remote" {
				n.Runtime = store.RuntimeRemote
			}
			if scenario == "missing node" {
				n.Name = "another"
			}
			if err := st.CreateNode(ctx, &n); err != nil {
				t.Fatal(err)
			}
			hash := fmt.Sprintf("%x", sha256.Sum256(data))
			desired, applied, applyErr := hash, hash, ""
			if scenario == "empty hash" {
				desired, applied = "", ""
			}
			if scenario == "wrong hash" {
				applied = "wrong"
			}
			if scenario == "apply error" {
				applyErr = "apply failed"
			}
			if scenario == "wrong config" {
				desired, applied = "wrong", "wrong"
			}
			if err := st.SetNodeDesired(ctx, n.ID, desired); err != nil {
				t.Fatal(err)
			}
			if err := st.SetNodeApplied(ctx, n.ID, applied, applyErr); err != nil {
				t.Fatal(err)
			}
			status := store.NodeOnline
			if scenario == "offline" {
				status = store.NodeOffline
			}
			seen := time.Now().Add(time.Hour)
			if scenario == "stale" {
				seen = time.Now().Add(-time.Hour)
			}
			if err := st.TouchNode(ctx, n.ID, status, seen); err != nil {
				t.Fatal(err)
			}
			if scenario == "bad key" {
				if err := os.WriteFile(filepath.Join(certDir, "agent.key"), []byte("bad"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "wrong CA" {
				if err := os.WriteFile(filepath.Join(certDir, "ca.crt"), []byte("bad"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "missing config" {
				if err := os.Remove(config); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			rt := &fakeRuntime{}
			var out bytes.Buffer
			err = runLocalNode([]string{"-check", "-db", dbPath, "-agent-cert", filepath.Join(certDir, "agent.crt"), "-config", config, "-timeout", "10ms"}, &out, rt)
			if scenario == "ready" {
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(out.String(), "ready") {
					t.Fatal(out.String())
				}
			} else if err == nil {
				t.Fatal("unready node accepted")
			}
			if rt.n != 0 {
				t.Fatal("readiness installed credentials")
			}
			after, err := os.ReadFile(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("readiness modified database")
			}
		})
	}
}

func TestLocalNodeCheckMissingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	err := runLocalNode([]string{"-ports", "-db", path, "-agent-cert", filepath.Join(filepath.Dir(path), "missing.crt"), "-timeout", "100ms"}, &bytes.Buffer{}, &fakeRuntime{})
	if err == nil {
		t.Fatal("missing database accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("readiness created database")
	}
}
