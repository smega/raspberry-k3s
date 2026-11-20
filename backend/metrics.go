package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// Prometheus metrics for the SLO burn-rate alerts, derived from the same JSON
// the page shows, so the alerts and the page can never disagree. Served on a
// separate port that no HTTPRoute points at, so it stays in-cluster.

type metricsService struct {
	Name          string `json:"name"`
	OK            bool   `json:"ok"`
	LatencyMs     *int   `json:"latencyMs"`
	LastCheckedAt string `json:"lastCheckedAt"`
	Error         string `json:"error"`
	SLO           *struct {
		TargetPercent          float64  `json:"targetPercent"`
		DaysOfData             int      `json:"daysOfData"`
		BudgetRemainingPercent *float64 `json:"budgetRemainingPercent"`
	} `json:"slo"`
}

func writeMetrics(w http.ResponseWriter, dataFile string) {
	var b strings.Builder
	b.WriteString("# HELP status_data_readable 1 if the checker's data file could be read and parsed.\n")
	b.WriteString("# TYPE status_data_readable gauge\n")

	raw, err := os.ReadFile(dataFile)
	var data struct {
		GeneratedAt string           `json:"generatedAt"`
		Services    []metricsService `json:"services"`
	}
	if err == nil {
		err = json.Unmarshal(raw, &data)
	}
	if err != nil {
		b.WriteString("status_data_readable 0\n")
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.Write([]byte(b.String()))
		return
	}
	b.WriteString("status_data_readable 1\n")

	if t, err := time.Parse(time.RFC3339Nano, data.GeneratedAt); err == nil {
		b.WriteString("# HELP status_data_generated_timestamp_seconds When the checker last wrote its results.\n")
		b.WriteString("# TYPE status_data_generated_timestamp_seconds gauge\n")
		fmt.Fprintf(&b, "status_data_generated_timestamp_seconds %d\n", t.Unix())
	}

	b.WriteString("# HELP status_service_up 1 if the service's last check passed.\n")
	b.WriteString("# TYPE status_service_up gauge\n")
	for _, s := range data.Services {
		up := 0
		if s.OK {
			up = 1
		}
		fmt.Fprintf(&b, "status_service_up{%s} %d\n", serviceLabels(s.Name), up)
	}

	b.WriteString("# HELP status_service_latency_seconds Response time of the service's last check.\n")
	b.WriteString("# TYPE status_service_latency_seconds gauge\n")
	for _, s := range data.Services {
		if s.LatencyMs != nil {
			fmt.Fprintf(&b, "status_service_latency_seconds{%s} %g\n", serviceLabels(s.Name), float64(*s.LatencyMs)/1000)
		}
	}

	// Why the last check failed, only while it is failing. The checker keeps
	// the reason for 24 hours; as a series, Prometheus keeps it for 30 days,
	// so a single failed check can still be explained a week later. The
	// values are exception class names or "HTTP <code>", a short list.
	b.WriteString("# HELP status_service_error 1 while the service's last check failed, with the reason.\n")
	b.WriteString("# TYPE status_service_error gauge\n")
	for _, s := range data.Services {
		if !s.OK && s.Error != "" {
			reason := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s.Error)
			fmt.Fprintf(&b, "status_service_error{%s,error=\"%s\"} 1\n", serviceLabels(s.Name), reason)
		}
	}

	// The 30-day error budget as the checker computes it from its daily
	// rollups: the same number the page shows. Below 0 the budget is
	// overspent. The deploy freeze (deployments/15-deploy-freeze.yaml)
	// is decided on this.
	b.WriteString("# HELP status_service_error_budget_remaining_ratio Share of the 30-day error budget left, 1 = untouched, below 0 = overspent.\n")
	b.WriteString("# TYPE status_service_error_budget_remaining_ratio gauge\n")
	for _, s := range data.Services {
		if s.SLO != nil && s.SLO.BudgetRemainingPercent != nil {
			fmt.Fprintf(&b, "status_service_error_budget_remaining_ratio{%s} %g\n", serviceLabels(s.Name), *s.SLO.BudgetRemainingPercent/100)
		}
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.Write([]byte(b.String()))
}

func serviceLabels(name string) string {
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(name)
	return fmt.Sprintf(`service="%s",slug="%s"`, esc, slugify(name))
}
