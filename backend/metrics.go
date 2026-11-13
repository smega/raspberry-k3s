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

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.Write([]byte(b.String()))
}

func serviceLabels(name string) string {
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(name)
	return fmt.Sprintf(`service="%s",slug="%s"`, esc, slugify(name))
}
