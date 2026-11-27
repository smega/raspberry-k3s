// node-remediator cordons and drains a worker that Node Problem Detector
// marks as sick, and uncordons it when the problem has been gone for a
// while. Two incidents ( 2026-11-23, 2026-11-26 ) were a slow SD card:
// the node stayed Ready, so Kubernetes moved nothing, and a human had to
// drain it. This does that drain, carefully: one node at a time, never the
// control plane, never when it would leave fewer than two healthy workers,
// and it tells a human through NodeAutoCordoned.
//
// It reads node conditions only, not Prometheus: on 2026-11-23 the service
// that reported pod states ran on the sick node too.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type metrics struct {
	mu        sync.Mutex
	lastRun   time.Time
	lastOK    bool
	cordoned  map[string]bool
	blocked   map[string]string
	podsLeft  map[string]int
	podsStuck map[string]int
	actions   map[string]int
	dryRun    bool
}

func (m *metrics) write(w http.ResponseWriter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := &strings.Builder{}
	ok := 0
	if m.lastOK {
		ok = 1
	}
	dry := 0
	if m.dryRun {
		dry = 1
	}
	fmt.Fprintf(b, "# HELP node_remediator_last_run_timestamp_seconds When the last round finished.\n# TYPE node_remediator_last_run_timestamp_seconds gauge\nnode_remediator_last_run_timestamp_seconds %d\n", m.lastRun.Unix())
	fmt.Fprintf(b, "# HELP node_remediator_last_run_success 1 if the last round read and did everything it meant to.\n# TYPE node_remediator_last_run_success gauge\nnode_remediator_last_run_success %d\n", ok)
	fmt.Fprintf(b, "# HELP node_remediator_dry_run 1 if it only logs what it would do.\n# TYPE node_remediator_dry_run gauge\nnode_remediator_dry_run %d\n", dry)
	b.WriteString("# HELP node_remediator_cordoned 1 for a node this remediator has cordoned.\n# TYPE node_remediator_cordoned gauge\n")
	for _, n := range sortedKeys(m.cordoned) {
		fmt.Fprintf(b, "node_remediator_cordoned{node=%q} 1\n", n)
	}
	b.WriteString("# HELP node_remediator_blocked 1 for a node with a problem that is left alone, with the reason.\n# TYPE node_remediator_blocked gauge\n")
	for _, n := range sortedKeys(m.blocked) {
		fmt.Fprintf(b, "node_remediator_blocked{node=%q,reason=%q} 1\n", n, m.blocked[n])
	}
	b.WriteString("# HELP node_remediator_pods_left Pods still to be evicted from a node we cordoned.\n# TYPE node_remediator_pods_left gauge\n")
	for _, n := range sortedKeys(m.podsLeft) {
		fmt.Fprintf(b, "node_remediator_pods_left{node=%q} %d\n", n, m.podsLeft[n])
	}
	b.WriteString("# HELP node_remediator_pods_pinned Pods left on a node we cordoned because their volume is on that node.\n# TYPE node_remediator_pods_pinned gauge\n")
	for _, n := range sortedKeys(m.podsStuck) {
		fmt.Fprintf(b, "node_remediator_pods_pinned{node=%q} %d\n", n, m.podsStuck[n])
	}
	b.WriteString("# HELP node_remediator_actions_total Cordons, uncordons and evictions done.\n# TYPE node_remediator_actions_total counter\n")
	for _, a := range []string{"cordon", "uncordon", "evict"} {
		fmt.Fprintf(b, "node_remediator_actions_total{action=%q} %d\n", a, m.actions[a])
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.Write([]byte(b.String()))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type remediator struct {
	kube   *kube
	cfg    config
	dryRun bool
	m      *metrics
}

// round reads every node, decides, acts, and drains what we cordoned.
func (r *remediator) round(ctx context.Context) error {
	nodes, err := r.kube.nodes(ctx)
	if err != nil {
		return err
	}
	byName := map[string]node{}
	views := make([]nodeView, 0, len(nodes))
	for _, n := range nodes {
		byName[n.Metadata.Name] = n
		views = append(views, view(n, r.cfg.Conditions))
	}
	now := time.Now()
	p := decide(views, now, r.cfg)
	stamp := now.UTC().Format(time.RFC3339)
	var errs []string

	for _, name := range p.Forget {
		log.Printf("%s: uncordoned by someone else, dropping our annotation", name)
		if !r.dryRun {
			patch := map[string]any{"metadata": map[string]any{"annotations": map[string]any{
				annCordonedBy: nil, annReason: nil, annCordonedAt: nil, annUncordonedAt: stamp,
			}}}
			if err := r.kube.patchNode(ctx, name, patch); err != nil {
				errs = append(errs, err.Error())
			}
		}
	}
	for _, name := range p.Uncordon {
		log.Printf("%s: healthy for %s, uncordoning", name, r.cfg.UncordonAfter)
		if r.dryRun {
			continue
		}
		patch := map[string]any{
			"spec": map[string]any{"unschedulable": false},
			"metadata": map[string]any{"annotations": map[string]any{
				annCordonedBy: nil, annReason: nil, annCordonedAt: nil, annUncordonedAt: stamp,
			}},
		}
		if err := r.kube.patchNode(ctx, name, patch); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		r.count("uncordon")
		r.kube.event(ctx, byName[name], "Normal", "AutoUncordoned", "healthy again for "+r.cfg.UncordonAfter.String())
	}
	cordonedNow := ""
	if p.Cordon != "" {
		log.Printf("%s: cordoning and draining ( %s )", p.Cordon, p.Reason)
		if !r.dryRun {
			patch := map[string]any{
				"spec": map[string]any{"unschedulable": true},
				"metadata": map[string]any{"annotations": map[string]any{
					annCordonedBy: me, annReason: truncate(p.Reason, 200), annCordonedAt: stamp,
				}},
			}
			if err := r.kube.patchNode(ctx, p.Cordon, patch); err != nil {
				errs = append(errs, err.Error())
			} else {
				r.count("cordon")
				cordonedNow = p.Cordon
				r.kube.event(ctx, byName[p.Cordon], "Warning", "AutoCordoned", truncate(p.Reason, 500))
			}
		}
	}
	for name, why := range p.Blocked {
		log.Printf("%s: has a problem, left alone: %s", name, why)
	}

	// Drain every node we hold, this round's included. Evictions a PDB
	// refuses are retried next round.
	cordoned, left, stuck := map[string]bool{}, map[string]int{}, map[string]int{}
	uncordoned := map[string]bool{}
	for _, n := range p.Uncordon {
		uncordoned[n] = true
	}
	for _, v := range views {
		if !((v.Ours && v.Unschedulable && !uncordoned[v.Name]) || v.Name == cordonedNow) {
			continue
		}
		cordoned[v.Name] = true
		l, s, err := r.drain(ctx, v.Name)
		if err != nil {
			errs = append(errs, err.Error())
		}
		left[v.Name], stuck[v.Name] = l, s
	}

	r.m.mu.Lock()
	r.m.cordoned, r.m.blocked, r.m.podsLeft, r.m.podsStuck = cordoned, p.Blocked, left, stuck
	r.m.mu.Unlock()
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func (r *remediator) drain(ctx context.Context, name string) (left, pinned int, err error) {
	pods, err := r.kube.podsOn(ctx, name)
	if err != nil {
		return 0, 0, err
	}
	for _, p := range pods {
		if ok, _ := evictable(p); !ok {
			continue
		}
		pin, err := r.kube.pinned(ctx, p)
		if err != nil {
			return left, pinned, err
		}
		if pin {
			pinned++
			continue
		}
		if r.dryRun {
			log.Printf("%s: would evict %s/%s", name, p.Metadata.Namespace, p.Metadata.Name)
			left++
			continue
		}
		done, err := r.kube.evict(ctx, p)
		switch {
		case err != nil:
			log.Printf("%s: evicting %s/%s: %v", name, p.Metadata.Namespace, p.Metadata.Name, err)
			left++
		case !done:
			left++ // a disruption budget says not yet
		default:
			r.count("evict")
			log.Printf("%s: evicted %s/%s", name, p.Metadata.Namespace, p.Metadata.Name)
		}
	}
	return left, pinned, nil
}

func (r *remediator) count(action string) {
	r.m.mu.Lock()
	r.m.actions[action]++
	r.m.mu.Unlock()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func duration(name, def string) time.Duration {
	d, err := time.ParseDuration(env(name, def))
	if err != nil {
		log.Fatalf("%s: %v", name, err)
	}
	return d
}

func main() {
	minOthers, err := strconv.Atoi(env("MIN_HEALTHY_OTHERS", "2"))
	if err != nil {
		log.Fatalf("MIN_HEALTHY_OTHERS: %v", err)
	}
	cfg := config{
		Conditions:       strings.Split(env("CONDITIONS", "SDCardSlow"), ","),
		ActAfter:         duration("ACT_AFTER", "2m"),
		UncordonAfter:    duration("UNCORDON_AFTER", "15m"),
		Cooldown:         duration("COOLDOWN", "6h"),
		MinHealthyOthers: minOthers,
	}
	interval := duration("INTERVAL", "30s")
	dry := os.Getenv("DRY_RUN") == "1"
	k, err := inCluster()
	if err != nil {
		log.Fatalf("kubernetes: %v", err)
	}
	m := &metrics{actions: map[string]int{}, dryRun: dry}
	r := &remediator{kube: k, cfg: cfg, dryRun: dry, m: m}

	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			err := r.round(ctx)
			cancel()
			if err != nil {
				log.Printf("round: %v", err)
			}
			m.mu.Lock()
			m.lastRun, m.lastOK = time.Now(), err == nil
			m.mu.Unlock()
			time.Sleep(interval)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) { m.write(w) })
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok\n")) })
	log.Printf("node-remediator: watching %v, dry run %v, listening on :8080", cfg.Conditions, dry)
	srv := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
