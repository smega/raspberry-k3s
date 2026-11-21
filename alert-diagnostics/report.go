package main

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// target is what an alert is about, from its labels.
type target struct {
	namespace string
	pod       string
	// A workload's name ( deployment, statefulset, ... ): its pods are
	// <name>-..., and so are most of its events.
	workload string
	node     string
	service  string
}

var workloadLabels = []string{"deployment", "statefulset", "daemonset", "job_name", "horizontalpodautoscaler", "cronjob"}

func targetOf(labels map[string]string) target {
	t := target{
		namespace: labels["namespace"],
		pod:       labels["pod"],
		node:      labels["node"],
		service:   labels["service"],
	}
	for _, l := range workloadLabels {
		if v := labels[l]; v != "" {
			t.workload = v
			break
		}
	}
	return t
}

// report builds the message. Every section is best effort: one that can't
// be read says so and the rest still goes out.
func (s *server) report(ctx context.Context, a alert) string {
	t := targetOf(a.Labels)
	if t.node == "" {
		t.node = s.nodeOfInstance(ctx, a.Labels["instance"])
	}
	now := time.Now()

	var b strings.Builder
	fmt.Fprintf(&b, "%s ( %s )", a.Labels["alertname"], a.Labels["severity"])
	for _, kv := range [][2]string{{"ns", t.namespace}, {"pod", t.pod}, {"workload", t.workload}, {"node", t.node}, {"service", t.service}} {
		if kv[1] != "" {
			fmt.Fprintf(&b, " %s=%s", kv[0], kv[1])
		}
	}
	if !a.StartsAt.IsZero() {
		fmt.Fprintf(&b, "\nfiring since %s UTC ( %s )", a.StartsAt.UTC().Format("15:04"), ago(now, a.StartsAt))
	}
	if sum := a.Annotations["summary"]; sum != "" {
		b.WriteString("\n" + sum)
	}

	section(&b, "Events", s.events(ctx, t, now))
	section(&b, "Logs", s.logs(ctx, t, now))
	section(&b, "Last change", s.lastChange(ctx, t, now))
	section(&b, "SLO and other alerts", s.slo(ctx, t, a.Labels["alertname"]))
	return b.String()
}

func section(b *strings.Builder, title string, lines []string) {
	if len(lines) == 0 {
		return
	}
	b.WriteString("\n\n" + title + ":\n" + strings.Join(lines, "\n"))
}

func ago(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// nodeOfInstance maps a node-exporter style instance ( "192.168.2.211:9100" )
// to the node with that address.
func (s *server) nodeOfInstance(ctx context.Context, instance string) string {
	host, _, err := net.SplitHostPort(instance)
	if err != nil || net.ParseIP(host) == nil {
		return ""
	}
	var nodes struct {
		Items []struct {
			Metadata struct{ Name string } `json:"metadata"`
			Status   struct {
				Addresses []struct{ Address string } `json:"addresses"`
			} `json:"status"`
		} `json:"items"`
	}
	if s.kube.get(ctx, "/api/v1/nodes", nil, &nodes) != nil {
		return ""
	}
	for _, n := range nodes.Items {
		for _, addr := range n.Status.Addresses {
			if addr.Address == host {
				return n.Metadata.Name
			}
		}
	}
	return ""
}

type kubeEvent struct {
	Type           string `json:"type"`
	Reason         string `json:"reason"`
	Message        string `json:"message"`
	Count          int    `json:"count"`
	LastTimestamp  string `json:"lastTimestamp"`
	EventTime      string `json:"eventTime"`
	InvolvedObject struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	} `json:"involvedObject"`
	Metadata struct {
		CreationTimestamp string `json:"creationTimestamp"`
	} `json:"metadata"`
}

func (e kubeEvent) when() time.Time {
	for _, s := range []string{e.LastTimestamp, e.EventTime, e.Metadata.CreationTimestamp} {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// matchEvents picks the events about the target, newest first: the pod's,
// the workload's ( and its pods' ), or else the namespace's warnings.
func matchEvents(events []kubeEvent, t target) []kubeEvent {
	var out []kubeEvent
	for _, e := range events {
		name := e.InvolvedObject.Name
		switch {
		case t.pod != "":
			if name != t.pod {
				continue
			}
		case t.workload != "":
			if name != t.workload && !strings.HasPrefix(name, t.workload+"-") {
				continue
			}
		default:
			if e.Type != "Warning" {
				continue
			}
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].when().After(out[j].when()) })
	return out
}

func (s *server) events(ctx context.Context, t target, now time.Time) []string {
	var lines []string
	add := func(evs []kubeEvent, max int) {
		for i, e := range evs {
			if i == max {
				break
			}
			count := ""
			if e.Count > 1 {
				count = fmt.Sprintf(" x%d", e.Count)
			}
			lines = append(lines, fmt.Sprintf("- %s %s %s/%s%s: %s", ago(now, e.when()), e.Reason,
				e.InvolvedObject.Kind, e.InvolvedObject.Name, count, oneLine(e.Message, 140)))
		}
	}
	if t.namespace != "" {
		var list struct{ Items []kubeEvent }
		if err := s.kube.get(ctx, "/api/v1/namespaces/"+url.PathEscape(t.namespace)+"/events", nil, &list); err != nil {
			lines = append(lines, "( could not read: "+err.Error()+" )")
		} else {
			add(matchEvents(list.Items, t), 5)
		}
	}
	if t.node != "" {
		var list struct{ Items []kubeEvent }
		q := url.Values{"fieldSelector": {"involvedObject.kind=Node,involvedObject.name=" + t.node}}
		if err := s.kube.get(ctx, "/api/v1/events", q, &list); err == nil {
			add(matchEvents(list.Items, target{pod: t.node}), 3)
		}
		lines = append(lines, s.nodeConditions(ctx, t.node)...)
	}
	if len(lines) == 0 && (t.namespace != "" || t.node != "") {
		lines = append(lines, "- none in the last hour")
	}
	return lines
}

func (s *server) nodeConditions(ctx context.Context, node string) []string {
	var n struct {
		Spec struct {
			Unschedulable bool `json:"unschedulable"`
		} `json:"spec"`
		Status struct {
			Conditions []struct{ Type, Status string } `json:"conditions"`
		} `json:"status"`
	}
	if err := s.kube.get(ctx, "/api/v1/nodes/"+url.PathEscape(node), nil, &n); err != nil {
		return []string{"( node " + node + ": " + err.Error() + " )"}
	}
	var bad []string
	for _, c := range n.Status.Conditions {
		if (c.Type == "Ready") != (c.Status == "True") {
			bad = append(bad, c.Type+"="+c.Status)
		}
	}
	if n.Spec.Unschedulable {
		bad = append(bad, "cordoned")
	}
	if len(bad) == 0 {
		return []string{"- node " + node + ": Ready, no pressure"}
	}
	return []string{"- node " + node + ": " + strings.Join(bad, ", ")}
}

// logQuery is the LogQL for the target's recent lines: its pod, its
// workload's pods, or errors anywhere in its namespace.
func logQuery(t target) string {
	ns := "namespace=" + strconv.Quote(t.namespace)
	switch {
	case t.namespace == "":
		return ""
	case t.pod != "":
		return "{" + ns + ", pod=" + strconv.Quote(t.pod) + "}"
	case t.workload != "":
		return "{" + ns + ", pod=~" + strconv.Quote(regexp.QuoteMeta(t.workload)+"-.*") + "}"
	}
	return "{" + ns + `} |~ "(?i)(error|fail|panic|fatal|refused|timeout)"`
}

func (s *server) logs(ctx context.Context, t target, now time.Time) []string {
	q := logQuery(t)
	if q == "" {
		return nil
	}
	var resp struct {
		Data struct {
			Result []struct {
				Stream map[string]string `json:"stream"`
				Values [][2]string       `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	params := url.Values{
		"query":     {q},
		"limit":     {"8"},
		"direction": {"backward"},
		"start":     {strconv.FormatInt(now.Add(-15*time.Minute).UnixNano(), 10)},
		"end":       {strconv.FormatInt(now.UnixNano(), 10)},
	}
	if err := s.loki.get(ctx, "/loki/api/v1/query_range", params, &resp); err != nil {
		return []string{"( could not read: " + err.Error() + " )"}
	}
	type line struct {
		ts   int64
		text string
	}
	var all []line
	for _, r := range resp.Data.Result {
		for _, v := range r.Values {
			ts, _ := strconv.ParseInt(v[0], 10, 64)
			prefix := ""
			if t.pod == "" {
				prefix = r.Stream["pod"] + ": "
			}
			all = append(all, line{ts, prefix + oneLine(v[1], 160)})
		}
	}
	if len(all) == 0 {
		return []string{"- nothing in the last 15 minutes"}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ts > all[j].ts })
	if len(all) > 8 {
		all = all[:8]
	}
	out := make([]string, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		out = append(out, time.Unix(0, all[i].ts).UTC().Format("15:04:05")+" "+all[i].text)
	}
	return out
}

func (s *server) lastChange(ctx context.Context, t target, now time.Time) []string {
	var lines []string
	if t.namespace != "" {
		var rs struct {
			Items []struct {
				Metadata struct {
					Name              string `json:"name"`
					CreationTimestamp string `json:"creationTimestamp"`
				} `json:"metadata"`
				Spec struct {
					Template struct {
						Spec struct {
							Containers []struct{ Image string } `json:"containers"`
						} `json:"spec"`
					} `json:"template"`
				} `json:"spec"`
			} `json:"items"`
		}
		if err := s.kube.get(ctx, "/apis/apps/v1/namespaces/"+url.PathEscape(t.namespace)+"/replicasets", nil, &rs); err == nil && len(rs.Items) > 0 {
			sort.Slice(rs.Items, func(i, j int) bool {
				return rs.Items[i].Metadata.CreationTimestamp > rs.Items[j].Metadata.CreationTimestamp
			})
			// The target's own rollouts if it has any, else the namespace's.
			newest := rs.Items[0]
			for _, r := range rs.Items {
				n := r.Metadata.Name
				if (t.pod != "" && strings.HasPrefix(t.pod, n+"-")) || (t.workload != "" && strings.HasPrefix(n, t.workload+"-")) {
					newest = r
					break
				}
			}
			created, _ := time.Parse(time.RFC3339, newest.Metadata.CreationTimestamp)
			var images []string
			for _, c := range newest.Spec.Template.Spec.Containers {
				images = append(images, shortImage(c.Image))
			}
			lines = append(lines, fmt.Sprintf("- last rollout in %s: %s, %s ( %s )", t.namespace,
				newest.Metadata.Name, ago(now, created), strings.Join(images, ", ")))
		}
	}
	// When Flux fetched the revision it runs now: a push to main shows here
	// within a minute.
	var repo struct {
		Status struct {
			Artifact struct {
				Revision       string `json:"revision"`
				LastUpdateTime string `json:"lastUpdateTime"`
			} `json:"artifact"`
		} `json:"status"`
	}
	if err := s.kube.get(ctx, "/apis/source.toolkit.fluxcd.io/v1/namespaces/flux-system/gitrepositories/flux-system", nil, &repo); err == nil {
		if at, err := time.Parse(time.RFC3339, repo.Status.Artifact.LastUpdateTime); err == nil {
			lines = append(lines, fmt.Sprintf("- git: %s, fetched by Flux %s", shortRevision(repo.Status.Artifact.Revision), ago(now, at)))
		}
	}
	var ks struct {
		Items []struct {
			Metadata struct{ Name string } `json:"metadata"`
			Status   struct {
				Conditions []struct{ Type, Status string } `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := s.kube.get(ctx, "/apis/kustomize.toolkit.fluxcd.io/v1/namespaces/flux-system/kustomizations", nil, &ks); err == nil {
		var notReady []string
		for _, k := range ks.Items {
			for _, c := range k.Status.Conditions {
				if c.Type == "Ready" && c.Status != "True" {
					notReady = append(notReady, k.Metadata.Name)
				}
			}
		}
		if len(notReady) > 0 {
			lines = append(lines, "- Flux not ready: "+strings.Join(notReady, ", "))
		}
	}
	return lines
}

// shortImage keeps the repository's last part and the tag:
// registry/raspberry/status-web:main-1-abc@sha256:... -> status-web:main-1-abc.
func shortImage(image string) string {
	image, _, _ = strings.Cut(image, "@")
	if i := strings.LastIndexByte(image, '/'); i >= 0 {
		image = image[i+1:]
	}
	return image
}

// shortRevision: main@sha1:0123456789abcdef... -> main@0123456.
func shortRevision(rev string) string {
	branch, hash, ok := strings.Cut(rev, "@sha1:")
	if !ok || len(hash) < 7 {
		return rev
	}
	return branch + "@" + hash[:7]
}

func (s *server) slo(ctx context.Context, t target, self string) []string {
	var lines []string
	var budgets struct {
		Data struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Value  [2]any            `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	// min by service: each status-api replica exports the same numbers.
	q := url.Values{"query": {"min by (service) (status_service_error_budget_remaining_ratio)"}}
	if err := s.prom.get(ctx, "/api/v1/query", q, &budgets); err == nil && len(budgets.Data.Result) > 0 {
		type budget struct {
			service string
			left    float64
		}
		var all []budget
		for _, r := range budgets.Data.Result {
			str, _ := r.Value[1].(string)
			v, err := strconv.ParseFloat(str, 64)
			if err == nil {
				all = append(all, budget{r.Metric["service"], v})
			}
		}
		sort.Slice(all, func(i, j int) bool { return all[i].left < all[j].left })
		var shown []string
		for i, bu := range all {
			if i < 2 || bu.service == t.service {
				shown = append(shown, fmt.Sprintf("%s %.0f%%", bu.service, bu.left*100))
			}
		}
		lines = append(lines, "- error budget left, lowest: "+strings.Join(shown, ", "))
	}

	var firing struct {
		Data struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
			} `json:"result"`
		} `json:"data"`
	}
	q = url.Values{"query": {`ALERTS{alertstate="firing", alertname!~"Watchdog|InfoInhibitor|NodeMaintenance"}`}}
	if err := s.prom.get(ctx, "/api/v1/query", q, &firing); err == nil {
		seen := map[string]bool{}
		var others []string
		for _, r := range firing.Data.Result {
			name := r.Metric["alertname"]
			if ns := r.Metric["namespace"]; ns != "" {
				name += " ( " + ns + " )"
			}
			if r.Metric["alertname"] == self || seen[name] {
				continue
			}
			seen[name] = true
			others = append(others, name)
		}
		sort.Strings(others)
		if len(others) == 0 {
			lines = append(lines, "- nothing else firing")
		} else {
			lines = append(lines, "- also firing: "+strings.Join(others, ", "))
		}
	}
	return lines
}
