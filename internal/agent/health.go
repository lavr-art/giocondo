package agent

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// readLoad1 returns the one-minute load average from /proc/loadavg.
func readLoad1(path string) float64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	f := strings.Fields(string(data))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}

// readKB reads kilobyte values from files like /proc/meminfo and /proc/self/status
// ("Key:   123 kB") and returns them in bytes.
func readKB(path string, keys ...string) map[string]int64 {
	out := map[string]int64{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer func() { _ = f.Close() }()
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok || !want[key] {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		out[key] = v * 1024
	}
	return out
}
