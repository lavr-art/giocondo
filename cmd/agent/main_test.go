package main

import (
	"log/slog"
	"reflect"
	"testing"
)

func TestLoadConfigPanelAddrs(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	c, err := loadConfig(env(map[string]string{"AGENT_PANEL_ADDR": "a.example.com:9443, 10.0.0.1:9443,"}))
	if err != nil {
		t.Fatal(err)
	}
	want := []panelAddr{{"a.example.com:9443", "a.example.com"}, {"10.0.0.1:9443", "10.0.0.1"}}
	if !reflect.DeepEqual(c.panels, want) {
		t.Errorf("panels = %+v, want %+v", c.panels, want)
	}

	c, err = loadConfig(env(map[string]string{"AGENT_PANEL_ADDR": "a:1,b:2", "AGENT_PANEL_SERVER_NAME": "panel.example.com"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range c.panels {
		if p.serverName != "panel.example.com" {
			t.Errorf("%s: server name %q", p.addr, p.serverName)
		}
	}

	for _, bad := range []string{"", " , ", "no-port"} {
		if _, err := loadConfig(env(map[string]string{"AGENT_PANEL_ADDR": bad})); err == nil {
			t.Errorf("AGENT_PANEL_ADDR=%q: no error", bad)
		}
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		in   string
		want slog.Level
	}{
		{"", slog.LevelInfo},
		{"info", slog.LevelInfo},
		{"DEBUG", slog.LevelDebug},
		{" warn ", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"bogus", slog.LevelInfo},
	}
	for _, tt := range tests {
		if got := parseLevel(tt.in); got != tt.want {
			t.Errorf("parseLevel(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
