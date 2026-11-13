package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Live per-node readings for the status page, pulled from the cluster's own
// Prometheus (kube-prometheus-stack, scraping node-exporter on every Pi).
// Only fixed queries are ever sent -- nothing from the incoming request
// reaches Prometheus -- and the response never contains node IPs or
// hostnames, only the same "Pi #N - role" labels cluster-dashboard uses.

const (
	nodesCacheTTL = 15 * time.Second
	historyWindow = 24 * time.Hour
	historyStep   = 15 * time.Minute
)

// Shared by the instant readings and the 24h history, so the chart always
// ends on the number shown above it.
const (
	tempQuery = `max by (instance) (node_thermal_zone_temp)`
	cpuQuery  = `100 * (1 - avg by (instance) (rate(node_cpu_seconds_total{mode="idle"}[2m])))`
	memQuery  = `100 * (1 - node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)`
)

type point struct {
	T int64   `json:"t"`
	V float64 `json:"v"`
}

type nodeStats struct {
	Name string `json:"name"`
	// Ready is the Kubernetes node condition; Reachable is whether
	// Prometheus could scrape the Pi's node-exporter on its last try. A Pi
	// that is powered off is neither, and its readings are all null.
	Ready          bool     `json:"ready"`
	Reachable      bool     `json:"reachable"`
	TempC          *float64 `json:"tempC"`
	CPUPercent     *float64 `json:"cpuPercent"`
	MemPercent     *float64 `json:"memPercent"`
	MemUsedBytes   *float64 `json:"memUsedBytes"`
	MemTotalBytes  *float64 `json:"memTotalBytes"`
	Load1          *float64 `json:"load1"`
	UptimeSeconds  *float64 `json:"uptimeSeconds"`
	CPUCores       *float64 `json:"cpuCores"`
	DiskUsedBytes  *float64 `json:"diskUsedBytes"`
	DiskTotalBytes *float64 `json:"diskTotalBytes"`
	PodsRunning    *float64 `json:"podsRunning"`
	TempHistory    []point  `json:"tempHistory"`
	CPUHistory     []point  `json:"cpuHistory"`
	MemHistory     []point  `json:"memHistory"`
}

// clusterInfo is the cluster-wide summary. Versions are cut to major.minor:
// enough to say what the cluster runs, without publishing exact patch levels.
type clusterInfo struct {
	CreatedAt   *int64   `json:"createdAt"`
	PodsRunning *float64 `json:"podsRunning"`
	Namespaces  *float64 `json:"namespaces"`
	Workloads   *float64 `json:"workloads"`
	K3s         string   `json:"k3s"`
	OS          string   `json:"os"`
	Kernel      string   `json:"kernel"`
	Runtime     string   `json:"runtime"`
}

type nodesResponse struct {
	GeneratedAt string      `json:"generatedAt"`
	Cluster     clusterInfo `json:"cluster"`
	Nodes       []nodeStats `json:"nodes"`
}

var majorMinor = regexp.MustCompile(`\d+\.\d+`)

// shortVersion keeps the leading text and major.minor of a version string:
// "v1.30.4+k3s1" -> "1.30", "containerd://1.7.20-k3s1" -> "containerd 1.7".
func shortVersion(name, v string) string {
	mm := majorMinor.FindString(v)
	if mm == "" {
		return ""
	}
	if name == "" {
		return mm
	}
	return name + " " + mm
}

// scalar runs an instant query expected to return a single value.
func (p *promClient) scalar(ctx context.Context, q string) *float64 {
	res, err := p.query(ctx, q)
	if err != nil || len(res) == 0 {
		return nil
	}
	v, ok := sampleValue(res[0].Value)
	if !ok {
		return nil
	}
	return &v
}

type promSample struct {
	Metric map[string]string `json:"metric"`
	Value  []any             `json:"value"`
	Values [][]any           `json:"values"`
}

type promClient struct {
	base string
	http *http.Client
}

func (p *promClient) get(ctx context.Context, path string, params url.Values) ([]promSample, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+path+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus %s: HTTP %d", path, resp.StatusCode)
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			Result []promSample `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if body.Status != "success" {
		return nil, fmt.Errorf("prometheus %s: status %q", path, body.Status)
	}
	return body.Data.Result, nil
}

func (p *promClient) query(ctx context.Context, q string) ([]promSample, error) {
	return p.get(ctx, "/api/v1/query", url.Values{"query": {q}})
}

// byInstance runs an instant query and returns its value per node-exporter
// instance label.
func (p *promClient) byInstance(ctx context.Context, q string) (map[string]float64, error) {
	res, err := p.query(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(res))
	for _, s := range res {
		if v, ok := sampleValue(s.Value); ok {
			out[s.Metric["instance"]] = v
		}
	}
	return out, nil
}

func sampleValue(pair []any) (float64, bool) {
	if len(pair) != 2 {
		return 0, false
	}
	str, ok := pair[1].(string)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseFloat(str, 64)
	return v, err == nil
}

func sampleTime(pair []any) (int64, bool) {
	if len(pair) != 2 {
		return 0, false
	}
	t, ok := pair[0].(float64)
	return int64(t), ok
}

// history runs a 24h range query and returns its points per instance.
// Failures only cost the chart, not the live readings.
func (p *promClient) history(ctx context.Context, q string, end time.Time) map[string][]point {
	res, err := p.get(ctx, "/api/v1/query_range", url.Values{
		"query": {q},
		"start": {strconv.FormatInt(end.Add(-historyWindow).Unix(), 10)},
		"end":   {strconv.FormatInt(end.Unix(), 10)},
		"step":  {strconv.Itoa(int(historyStep.Seconds()))},
	})
	out := map[string][]point{}
	if err != nil {
		return out
	}
	for _, s := range res {
		pts := make([]point, 0, len(s.Values))
		for _, pair := range s.Values {
			t, okT := sampleTime(pair)
			v, okV := sampleValue(pair)
			if okT && okV {
				pts = append(pts, point{T: t, V: round1(v)})
			}
		}
		out[s.Metric["instance"]] = pts
	}
	return out
}

func (p *promClient) collect(ctx context.Context) (*nodesResponse, error) {
	// The node list comes from kube-state-metrics, so a Pi that is down
	// still gets a card. last_over_time keeps the node -> node-exporter
	// instance mapping alive for a day after the exporter stops answering.
	kubeNodes, err := p.query(ctx, `kube_node_info`)
	if err != nil {
		return nil, err
	}
	unames, err := p.query(ctx, `last_over_time(node_uname_info[1d])`)
	if err != nil {
		return nil, err
	}
	instanceByNode := map[string]string{}
	for _, s := range unames {
		if s.Metric["nodename"] != "" {
			instanceByNode[s.Metric["nodename"]] = s.Metric["instance"]
		}
	}

	names := make([]string, 0, len(kubeNodes))
	for _, s := range kubeNodes {
		if s.Metric["node"] != "" {
			names = append(names, s.Metric["node"])
		}
	}
	if len(names) == 0 {
		// kube-state-metrics down: fall back to whatever node-exporter knows.
		for name := range instanceByNode {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	labelSet := func(q, label string) map[string]bool {
		out := map[string]bool{}
		if res, err := p.query(ctx, q); err == nil {
			for _, s := range res {
				if v, ok := sampleValue(s.Value); ok && v == 1 {
					out[s.Metric[label]] = true
				}
			}
		}
		return out
	}
	controlPlane := labelSet(`kube_node_role{role=~"control-plane|master"}`, "node")
	ready := labelSet(`kube_node_status_condition{condition="Ready",status="true"}`, "node")
	reachable := labelSet(`up{job=~".*node-exporter.*"}`, "instance")

	queries := map[string]string{
		"temp":      tempQuery,
		"cpu":       cpuQuery,
		"memTotal":  `node_memory_MemTotal_bytes`,
		"memUsed":   `node_memory_MemTotal_bytes - node_memory_MemAvailable_bytes`,
		"load1":     `node_load1`,
		"uptime":    `time() - node_boot_time_seconds`,
		"cores":     `count by (instance) (node_cpu_seconds_total{mode="idle"})`,
		"diskTotal": `node_filesystem_size_bytes{mountpoint="/"}`,
		"diskUsed":  `node_filesystem_size_bytes{mountpoint="/"} - node_filesystem_avail_bytes{mountpoint="/"}`,
	}
	values := make(map[string]map[string]float64, len(queries))
	for key, q := range queries {
		v, err := p.byInstance(ctx, q)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		values[key] = v
	}

	podsByNode := map[string]float64{}
	if res, err := p.query(ctx, `count by (node) (kube_pod_info * on (namespace, pod) group_left () (kube_pod_status_phase{phase="Running"} == 1))`); err == nil {
		for _, s := range res {
			if v, ok := sampleValue(s.Value); ok {
				podsByNode[s.Metric["node"]] = v
			}
		}
	}

	cluster := clusterInfo{
		PodsRunning: p.scalar(ctx, `sum(kube_pod_status_phase{phase="Running"})`),
		Namespaces:  p.scalar(ctx, `count(kube_namespace_status_phase{phase="Active"} == 1)`),
		Workloads:   p.scalar(ctx, `count(kube_deployment_created) + count(kube_statefulset_created) + count(kube_daemonset_created)`),
	}
	if v := p.scalar(ctx, `min(kube_node_created)`); v != nil {
		t := int64(*v)
		cluster.CreatedAt = &t
	}
	for _, s := range kubeNodes {
		if controlPlane[s.Metric["node"]] || cluster.K3s == "" {
			cluster.K3s = shortVersion("", s.Metric["kubelet_version"])
			cluster.OS = s.Metric["os_image"]
			cluster.Kernel = shortVersion("Linux", s.Metric["kernel_version"])
			if rt := s.Metric["container_runtime_version"]; rt != "" {
				name, _, _ := strings.Cut(rt, "://")
				cluster.Runtime = shortVersion(name, rt)
			}
		}
	}

	end := time.Now()
	tempHist := p.history(ctx, tempQuery, end)
	cpuHist := p.history(ctx, cpuQuery, end)
	memHist := p.history(ctx, memQuery, end)

	pick := func(key, inst string) *float64 {
		v, ok := values[key][inst]
		if !ok {
			return nil
		}
		r := round1(v)
		return &r
	}
	orEmpty := func(pts []point) []point {
		if pts == nil {
			return []point{}
		}
		return pts
	}

	resp := &nodesResponse{GeneratedAt: end.UTC().Format(time.RFC3339), Cluster: cluster, Nodes: []nodeStats{}}
	for i, name := range names {
		role := "worker"
		if controlPlane[name] {
			role = "control plane"
		}
		inst := instanceByNode[name]
		n := nodeStats{
			Name:        fmt.Sprintf("Pi #%d - %s", i+1, role),
			Ready:       ready[name],
			Reachable:   inst != "" && reachable[inst],
			TempHistory: orEmpty(tempHist[inst]),
			CPUHistory:  orEmpty(cpuHist[inst]),
			MemHistory:  orEmpty(memHist[inst]),
		}
		if n.Reachable {
			n.TempC = pick("temp", inst)
			n.CPUPercent = pick("cpu", inst)
			n.Load1 = pick("load1", inst)
			n.UptimeSeconds = pick("uptime", inst)
			if v, ok := values["cores"][inst]; ok {
				n.CPUCores = &v
			}
			if v, ok := values["diskTotal"][inst]; ok {
				n.DiskTotalBytes = &v
			}
			if v, ok := values["diskUsed"][inst]; ok {
				n.DiskUsedBytes = &v
			}
			if used, ok := values["memUsed"][inst]; ok {
				n.MemUsedBytes = &used
			}
			if total, ok := values["memTotal"][inst]; ok && total > 0 {
				n.MemTotalBytes = &total
				if n.MemUsedBytes != nil {
					pct := round1(100 * *n.MemUsedBytes / total)
					n.MemPercent = &pct
				}
			}
		}
		if v, ok := podsByNode[name]; ok {
			n.PodsRunning = &v
		}
		resp.Nodes = append(resp.Nodes, n)
	}
	return resp, nil
}

func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}

// nodesCache keeps the last good answer for nodesCacheTTL, so a public page
// left open in many tabs costs Prometheus one round of queries per 15s, not
// one per visitor poll.
type nodesCache struct {
	prom *promClient

	mu      sync.Mutex
	last    *nodesResponse
	fetched time.Time
}

func (c *nodesCache) get(ctx context.Context) (*nodesResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last != nil && time.Since(c.fetched) < nodesCacheTTL {
		return c.last, nil
	}
	resp, err := c.prom.collect(ctx)
	if err != nil {
		return nil, err
	}
	c.last, c.fetched = resp, time.Now()
	return resp, nil
}
