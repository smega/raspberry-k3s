package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"sync"
	"time"
)

// The four DORA metrics for status-web, the one service that ships through
// the canary. Everything is measured, nothing is entered by hand:
//
//   - a deploy is a build that reached production: the first primary pod
//     running its tag, or, for builds from before the canary, the first pod;
//   - lead time is from the commit (GitHub) to that pod;
//   - a failed change is a canary that Flagger rolled back, and its restore
//     time is how long the bad version ran before the rollback finished.
//
// Pod creation times come from kube-state-metrics, the tag from the pod's
// image (main-<build time>-<short sha>, see build-image.yml), so the window
// is as long as Prometheus keeps history. Platform changes (Kong, k3s) are
// not deploys of status-web; their failures are in the incident list.

const (
	doraCacheTTL = 10 * time.Minute
	doraWindow   = 30 * 24 * time.Hour
	// Creation time and image per status-web pod, over the window.
	doraPodsQuery = `min by (pod, image_spec) (
  last_over_time(kube_pod_created{namespace="public-demo",pod=~"status-web-.*"}[30d])
  * on (pod) group_left (image_spec)
  max by (pod, image_spec) (last_over_time(kube_pod_container_info{namespace="public-demo",container="web",image_spec=~".*/status-web:main-.*"}[30d]))
)`
	// Coarse, to find the rollbacks in 30 days (a 1m step would be over
	// Prometheus' 11,000 points): failed (2) stays until the next release,
	// so a max over each step still shows a short one. Each is then
	// measured at the scrape interval. A min would not do: a rollback and
	// the next release within one step merge into one run that ends in 1.
	// Until 2026-11-19 06:38 the PodMonitor lacked honorLabels and the
	// label was exported_namespace, so both are read.
	doraStatusQuery = `max(
  max_over_time(flagger_canary_status{namespace="public-demo",name="status-web"}[5m])
  or max_over_time(flagger_canary_status{exported_namespace="public-demo",name="status-web"}[5m])
)`
	doraFineQuery = `min(
  flagger_canary_status{namespace="public-demo",name="status-web"}
  or flagger_canary_status{exported_namespace="public-demo",name="status-web"}
)`
	doraStatusStep = 5 * time.Minute
	doraRepo       = "smega/raspberry-k3s"
)

var (
	tagRe     = regexp.MustCompile(`:main-\d+-([0-9a-f]{7,40})`)
	primaryRe = regexp.MustCompile(`^status-web-primary-`)
)

type doraInfo struct {
	WindowDays int `json:"windowDays"`
	// The first deploy in the window; history may be shorter than it.
	Since   *int64 `json:"since"`
	Deploys int    `json:"deploys"`
	// Median commit-to-production time, over the deploys GitHub could date.
	LeadTimeSeconds *int64 `json:"leadTimeSeconds"`
	Failed          int    `json:"failed"`
	// Failed over all finished changes (deploys plus rollbacks).
	ChangeFailureRate *float64 `json:"changeFailureRate"`
	// Median time from a failed release's start to its rollback.
	RestoreSeconds *int64 `json:"restoreSeconds"`
}

// commitTimes looks up commit dates on GitHub and keeps them: a commit's
// date never changes, so each sha costs one request ever, well inside the
// 60 an hour GitHub allows without a token.
type commitTimes struct {
	http *http.Client

	mu    sync.Mutex
	known map[string]int64
}

func (c *commitTimes) get(ctx context.Context, sha string) (int64, bool) {
	c.mu.Lock()
	t, ok := c.known[sha]
	c.mu.Unlock()
	if ok {
		return t, true
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("https://api.github.com/repos/%s/commits/%s", doraRepo, sha), nil)
	if err != nil {
		return 0, false
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, false
	}
	var body struct {
		Commit struct {
			Committer struct {
				Date time.Time `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Commit.Committer.Date.IsZero() {
		return 0, false
	}
	t = body.Commit.Committer.Date.Unix()
	c.mu.Lock()
	c.known[sha] = t
	c.mu.Unlock()
	return t, true
}

// build is one image tag of status-web and when it got where.
type build struct {
	sha        string
	firstSeen  int64 // first pod with it, canary or not
	firstPrime int64 // first primary pod with it, 0 if none
}

func median(xs []int64) *int64 {
	if len(xs) == 0 {
		return nil
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
	m := xs[len(xs)/2]
	if len(xs)%2 == 0 {
		m = (xs[len(xs)/2-1] + xs[len(xs)/2]) / 2
	}
	return &m
}

func (p *promClient) dora(ctx context.Context, commits *commitTimes) (*doraInfo, error) {
	end := time.Now()
	windowStart := end.Add(-doraWindow).Unix()
	info := &doraInfo{WindowDays: int(doraWindow.Hours() / 24)}

	pods, err := p.query(ctx, doraPodsQuery)
	if err != nil {
		return nil, err
	}
	builds := map[string]*build{}
	var firstPrimary int64
	for _, s := range pods {
		m := tagRe.FindStringSubmatch(s.Metric["image_spec"])
		v, ok := sampleValue(s.Value)
		if m == nil || !ok {
			continue
		}
		tag, created := m[0], int64(v)
		b := builds[tag]
		if b == nil {
			b = &build{sha: m[1], firstSeen: created}
			builds[tag] = b
		}
		b.firstSeen = min(b.firstSeen, created)
		if primaryRe.MatchString(s.Metric["pod"]) {
			if b.firstPrime == 0 || created < b.firstPrime {
				b.firstPrime = created
			}
			if firstPrimary == 0 || created < firstPrimary {
				firstPrimary = created
			}
		}
	}

	var leads []int64
	for _, b := range builds {
		// Before the canary there was no primary: the first pod was
		// production. After it, only a promotion counts.
		prod := b.firstPrime
		if firstPrimary == 0 || b.firstSeen < firstPrimary {
			prod = b.firstSeen
		}
		if prod == 0 || prod < windowStart {
			continue
		}
		info.Deploys++
		if info.Since == nil || prod < *info.Since {
			info.Since = &prod
		}
		if t, ok := commits.get(ctx, b.sha); ok && prod >= t {
			leads = append(leads, prod-t)
		}
	}
	info.LeadTimeSeconds = median(leads)

	ts, vs, err := p.statusSeries(ctx, doraStatusQuery, end.Add(-doraWindow), end, doraStatusStep)
	if err != nil {
		return nil, err
	}
	var restores []int64
	for i := 0; i < len(vs); i++ {
		if vs[i] != 2 || (i > 0 && vs[i-1] == 2) {
			continue
		}
		info.Failed++
		// The release that failed is the last run of 0s before this step;
		// an hour back covers any analysis (at most ~10 minutes here).
		from := time.Unix(ts[i], 0).Add(-time.Hour)
		to := time.Unix(ts[i], 0).Add(doraStatusStep)
		fts, fvs, err := p.statusSeries(ctx, doraFineQuery, from, to, fineStep)
		if err != nil {
			continue
		}
		var restore int64 = -1
		for _, f := range progressingRuns(fvs) {
			if f.end < len(fvs) && fvs[f.end] == 2 {
				restore = fts[f.end] - fts[f.start]
			}
		}
		if restore >= 0 {
			restores = append(restores, restore)
		}
	}
	info.RestoreSeconds = median(restores)
	if total := info.Deploys + info.Failed; total > 0 {
		rate := float64(info.Failed) / float64(total)
		info.ChangeFailureRate = &rate
	}
	return info, nil
}

type doraCache struct {
	prom    *promClient
	commits *commitTimes

	mu      sync.Mutex
	last    *doraInfo
	fetched time.Time
}

func (c *doraCache) get(ctx context.Context) (*doraInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last != nil && time.Since(c.fetched) < doraCacheTTL {
		return c.last, nil
	}
	resp, err := c.prom.dora(ctx, c.commits)
	if err != nil {
		return nil, err
	}
	c.last, c.fetched = resp, time.Now()
	return resp, nil
}
