package web

import (
	"fmt"
	"strconv"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/protocol"
)

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}

	units := []string{"KB", "MB", "GB", "TB", "PB"}
	v := float64(b) / unit

	i := 0
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}

	return strconv.FormatFloat(v, 'f', 1, 64) + " " + units[i]
}

func fmtDate(t *time.Time) string {
	if t == nil {
		return "no expiry"
	}

	return t.UTC().Format("2006-01-02")
}

func ago(t *time.Time) string {
	if t == nil {
		return "never"
	}

	d := time.Since(*t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}

	return fmt.Sprintf("%d d ago", int(d.Hours()/24))
}

func percent(used, limit int64) int {
	if limit <= 0 {
		return 0
	}

	return int(min(100, used*100/limit))
}

func statusLabel(s string) string {
	switch s {
	case string(panel.UserActive):
		return "active"
	case string(panel.UserDisabled):
		return "disabled"
	case string(panel.UserExpired):
		return "expired"
	case string(panel.UserLimited):
		return "out of traffic"
	case "online":
		return "online"
	case "offline":
		return "offline"
	case "new":
		return "waiting for agent"
	}

	return s
}

func protoLabel(t protocol.InboundType) string {
	switch t {
	case protocol.TypeVLESSReality:
		return "VLESS Reality"
	case protocol.TypeHysteria2:
		return "Hysteria2"
	case protocol.TypeWireGuard:
		return "WireGuard"
	}

	return string(t)
}
