package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/artemlavr/giocondo/internal/agent/engine"
)

func TestCheckJournalRejectsUncommittedAndChangedConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sing-box"), 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "config.json")
	if err := os.WriteFile(config, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"committed", "activating", "blocked"} {
		for _, hash := range []string{engine.Hash([]byte("old")), "wrong"} {
			j := `{"id":"sing-box","phase":"` + phase + `","applied_hash":"` + hash + `"}`
			if err := os.WriteFile(filepath.Join(dir, "sing-box/journal.json"), []byte(j), 0600); err != nil {
				t.Fatal(err)
			}
			err := checkJournal(dir, config)
			if (err == nil) != (phase == "committed" && hash == engine.Hash([]byte("old"))) {
				t.Fatalf("phase=%s hash=%s: %v", phase, hash, err)
			}
		}
	}
}
