package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/artemlavr/giocondo/internal/agent/engine"
	"github.com/artemlavr/giocondo/internal/agent/singboxdriver"
	"github.com/artemlavr/giocondo/internal/servicecheck"
)

func checkAgent(pid int) error {
	getenv, err := servicecheck.Environment(pid)
	if err != nil {
		return err
	}
	c, err := loadConfig(getenv)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d := &singboxdriver.Driver{Binary: c.singBoxBin, StatusCmd: c.statusCmd}
	if err := d.Validate(ctx, c.configPath); err != nil {
		return err
	}
	if active, err := d.Active(ctx); err != nil || !active {
		return errors.New("sing-box is not active")
	}
	return checkJournal(c.stateDir, c.configPath)
}

func checkJournal(stateDir, configPath string) error {
	b, err := os.ReadFile(filepath.Join(stateDir, "sing-box", "journal.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Legacy agents can run a configuration without having persisted their first journal yet.
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	var j struct {
		ID    string `json:"id"`
		Phase string `json:"phase"`
		Hash  string `json:"applied_hash"`
	}
	if err := json.Unmarshal(b, &j); err != nil {
		return errors.New("invalid engine journal")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	if j.ID != "sing-box" || j.Phase != "committed" || j.Hash != engine.Hash(data) {
		return fmt.Errorf("engine journal is not committed to the active configuration")
	}
	return nil
}
