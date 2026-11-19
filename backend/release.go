package main

import (
	"context"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// The last canary release of status-web, for the status page. Flagger
// keeps no start time or duration in the Canary object, but it exports the
// phase as flagger_canary_status (0 progressing, 1 promoted or idle, 2
// failed), so a release is a run of 0s in that series: it started where
// the run starts, and the value after the run says how it ended. Fixed
// queries only, like nodes.go.

const (
	releaseCacheTTL = 30 * time.Second
	releaseWindow   = 7 * 24 * time.Hour
	releaseStep     = time.Minute
	// min_over_time: a progressing phase shorter than the step still shows.
	// It also makes a release look up to a minute longer, so the last one is
	// measured again at the scrape interval (fineQuery, fineStep).
	releaseQuery = `min(min_over_time(flagger_canary_status{namespace="public-demo",name="status-web"}[1m]))`
	fineQuery    = `min(flagger_canary_status{namespace="public-demo",name="status-web"})`
	fineStep     = 15 * time.Second
	weightQuery  = `max(flagger_canary_weight{namespace="public-demo",workload="status-web"})`
)

type releaseInfo struct {
	// "promoted", "rolled back", "in progress", or "" when there was no
	// release in the window.
	Outcome         string `json:"outcome"`
	StartedAt       *int64 `json:"startedAt"`
	FinishedAt      *int64 `json:"finishedAt"`
	DurationSeconds *int64 `json:"durationSeconds"`
	// Share of the traffic on the new version right now (in progress only).
	CanaryWeight *float64 `json:"canaryWeight"`
	// Counts over the window, for "N releases this week, M rolled back".
	Releases   int `json:"releases"`
	RolledBack int `json:"rolledBack"`
	WindowDays int `json:"windowDays"`
}

// statusSeries runs a range query and returns its first series as
// parallel timestamp and value slices.
func (p *promClient) statusSeries(ctx context.Context, q string, start, end time.Time, step time.Duration) ([]int64, []float64, error) {
	res, err := p.get(ctx, "/api/v1/query_range", url.Values{
		"query": {q},
		"start": {strconv.FormatInt(start.Unix(), 10)},
		"end":   {strconv.FormatInt(end.Unix(), 10)},
		"step":  {strconv.Itoa(int(step.Seconds()))},
	})
	if err != nil || len(res) == 0 {
		return nil, nil, err
	}
	var ts []int64
	var vs []float64
	for _, pair := range res[0].Values {
		t, okT := sampleTime(pair)
		v, okV := sampleValue(pair)
		if okT && okV {
			ts, vs = append(ts, t), append(vs, v)
		}
	}
	return ts, vs, nil
}

// release is one run of 0s (progressing): where it starts, and the index
// of the first sample after it (len(vs) if it's still running).
type releaseRun struct{ start, end int }

func progressingRuns(vs []float64) []releaseRun {
	var runs []releaseRun
	for i := 0; i < len(vs); i++ {
		if vs[i] != 0 {
			continue
		}
		start := i
		for i < len(vs) && vs[i] == 0 {
			i++
		}
		runs = append(runs, releaseRun{start, i})
	}
	return runs
}

func (p *promClient) release(ctx context.Context) (*releaseInfo, error) {
	end := time.Now()
	info := &releaseInfo{WindowDays: int(releaseWindow.Hours() / 24)}
	ts, vs, err := p.statusSeries(ctx, releaseQuery, end.Add(-releaseWindow), end, releaseStep)
	if err != nil {
		return nil, err
	}
	runs := progressingRuns(vs)
	if len(runs) == 0 {
		return info, nil
	}
	info.Releases = len(runs)
	for _, r := range runs {
		if r.end < len(vs) && vs[r.end] == 2 {
			info.RolledBack++
		}
	}

	// The last release, measured again at the scrape interval around where
	// the coarse pass found it.
	last := runs[len(runs)-1]
	from := time.Unix(ts[last.start], 0).Add(-2 * releaseStep)
	if fts, fvs, err := p.statusSeries(ctx, fineQuery, from, end, fineStep); err == nil {
		if fine := progressingRuns(fvs); len(fine) > 0 {
			ts, vs, last = fts, fvs, fine[len(fine)-1]
		}
	}

	startedAt := ts[last.start]
	info.StartedAt = &startedAt
	if last.end == len(vs) {
		info.Outcome = "in progress"
		info.CanaryWeight = p.scalar(ctx, weightQuery)
		return info, nil
	}
	// The first sample after the run: 1 promoted, 2 failed.
	finishedAt := ts[last.end]
	duration := finishedAt - startedAt
	info.FinishedAt, info.DurationSeconds = &finishedAt, &duration
	info.Outcome = "promoted"
	if vs[last.end] == 2 {
		info.Outcome = "rolled back"
	}
	return info, nil
}

type releaseCache struct {
	prom *promClient

	mu      sync.Mutex
	last    *releaseInfo
	fetched time.Time
}

func (c *releaseCache) get(ctx context.Context) (*releaseInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last != nil && time.Since(c.fetched) < releaseCacheTTL {
		return c.last, nil
	}
	resp, err := c.prom.release(ctx)
	if err != nil {
		return nil, err
	}
	c.last, c.fetched = resp, time.Now()
	return resp, nil
}
