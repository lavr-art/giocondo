package agent

import (
	"github.com/artemlavr/giocondo/internal/agent/singboxdriver"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/artemlavr/giocondo/internal/nodeapi/v2raystats"
)

func TestParseStats(t *testing.T) {
	in := []*v2raystats.Stat{
		{Name: "user>>>alice>>>traffic>>>uplink", Value: 10},
		{Name: "user>>>alice>>>traffic>>>downlink", Value: 100},
		{Name: "outbound>>>wg-alice>>>traffic>>>uplink", Value: 1},
		{Name: "outbound>>>wg-alice>>>traffic>>>downlink", Value: 2},
		{Name: "outbound>>>wg-bob>>>traffic>>>downlink", Value: 5},
		{Name: "outbound>>>direct>>>traffic>>>uplink", Value: 999}, // not a user
		{Name: "inbound>>>vless>>>traffic>>>uplink", Value: 999},
		{Name: "user>>>carol>>>traffic>>>uplink", Value: 0}, // zero values are skipped
		{Name: "garbage", Value: 1},
		{Name: "user>>>dave>>>traffic>>>sideways", Value: 7},
	}
	got := singboxdriver.ParseStats(in)
	want := map[string]Traffic{"alice": {Up: 11, Down: 102}, "bob": {Down: 5}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAccumulator(t *testing.T) {
	var a Accumulator
	if a.Next() != nil {
		t.Fatal("empty accumulator returned stats")
	}
	a.Add(map[string]Traffic{"alice": {Up: 1, Down: 2}})
	a.Add(map[string]Traffic{"alice": {Up: 10}, "bob": {Down: 5}})
	s1 := a.Next()
	if s1 == nil || s1.Seq != 1 || len(s1.Users) != 2 || s1.Users[0].Name != "alice" || s1.Users[0].Up != 11 || s1.Users[0].Down != 2 {
		t.Fatalf("first batch %v", s1)
	}

	// Without an ack the same batch is sent again and new traffic waits.
	a.Add(map[string]Traffic{"alice": {Up: 100}})
	if again := a.Next(); again != s1 {
		t.Fatalf("unacked batch not repeated: %v", again)
	}
	a.Ack(99) // a foreign seq clears nothing
	if a.Next() != s1 {
		t.Fatal("wrong ack cleared batch")
	}
	a.Ack(1)
	s2 := a.Next()
	if s2 == nil || s2.Seq != 2 || len(s2.Users) != 1 || s2.Users[0].Up != 100 {
		t.Fatalf("second batch %v", s2)
	}
	a.Ack(2)
	if a.Next() != nil {
		t.Fatal("stats after everything was acked")
	}
}

func TestProcReaders(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "loadavg"), []byte("0.42 0.30 0.10 1/100 123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meminfo := "MemTotal:        1000 kB\nMemFree:          100 kB\nMemAvailable:     500 kB\n"
	if err := os.WriteFile(filepath.Join(dir, "meminfo"), []byte(meminfo), 0o644); err != nil {
		t.Fatal(err)
	}
	if v := readLoad1(filepath.Join(dir, "loadavg")); v != 0.42 {
		t.Errorf("load1 %v", v)
	}
	m := readKB(filepath.Join(dir, "meminfo"), "MemTotal", "MemAvailable")
	if m["MemTotal"] != 1000*1024 || m["MemAvailable"] != 500*1024 || len(m) != 2 {
		t.Errorf("meminfo %v", m)
	}
	if readLoad1(filepath.Join(dir, "missing")) != 0 || len(readKB(filepath.Join(dir, "missing"), "X")) != 0 {
		t.Error("missing files must give zero values")
	}
	// The real /proc/self/status of this process; /proc exists only on Linux.
	if runtime.GOOS != "linux" {
		return
	}
	if rss := readKB("/proc/self/status", "VmRSS")["VmRSS"]; rss <= 0 {
		t.Errorf("own rss %d", rss)
	}
}
