package web

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

const pageDashboard = "dashboard"

type dashboardData struct {
	panel.Dashboard

	// ChartData and ChartSeries are the JSON attributes of the Basecoat chart, with traffic in ChartUnit.
	ChartData   string
	ChartSeries string
	ChartUnit   string
}

func (h *Handler) dashboardPage(w http.ResponseWriter, r *http.Request) {
	d, err := h.Dashboard.Overview(r.Context())
	if err != nil {
		h.fail(w, r, err)

		return
	}

	data, err := newDashboardData(d)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	h.render(w, r, pageDashboard,
		page{Title: "Overview", Nav: pageDashboard, Authed: true, Flash: r.URL.Query().Get("flash"), Data: data},
		http.StatusOK)
}

// newDashboardData converts daily traffic into chart rows in the largest unit the busiest day reaches,
// so small installations do not show a chart of zeros.
func newDashboardData(d panel.Dashboard) (dashboardData, error) {
	var peak int64
	for _, day := range d.Days {
		peak = max(peak, day.Up, day.Down)
	}

	units := []string{"B", "KB", "MB", "GB", "TB"}
	exp := 0

	for exp < len(units)-1 && float64(peak) >= math.Pow(1024, float64(exp+1)) {
		exp++
	}

	div := math.Pow(1024, float64(exp))
	value := func(b int64) float64 { return math.Round(float64(b)/div*100) / 100 }

	type row struct {
		Label    string  `json:"label"`
		Download float64 `json:"download"`
		Upload   float64 `json:"upload"`
	}

	rows := make([]row, len(d.Days))
	for i, day := range d.Days {
		rows[i] = row{Label: day.Day.Format("Jan 2"), Download: value(day.Down), Upload: value(day.Up)}
	}

	type series struct {
		Label string `json:"label"`
		Color string `json:"color"`
	}

	unit := units[exp]
	seriesMap := map[string]series{
		"download": {Label: "Download, " + unit, Color: "var(--chart-2)"},
		"upload":   {Label: "Upload, " + unit, Color: "var(--chart-1)"},
	}

	rowsJSON, err := json.Marshal(rows)
	if err != nil {
		return dashboardData{}, fmt.Errorf("chart data: %w", err)
	}

	seriesJSON, err := json.Marshal(seriesMap)
	if err != nil {
		return dashboardData{}, fmt.Errorf("chart series: %w", err)
	}

	return dashboardData{Dashboard: d, ChartData: string(rowsJSON), ChartSeries: string(seriesJSON), ChartUnit: unit}, nil
}
