package main

import (
	"context"
	"sync"
	"time"
)

// What visitors actually got over the last day, from Kong's per-request
// metrics ( kong/metrics.yaml ), for the status page: the same numbers the
// traffic SLOs in deployments/17-traffic-slo.yaml alert on. Fixed queries
// only, like nodes.go.

const (
	trafficCacheTTL = time.Minute
	trafficRequests = `sum(increase(kong_http_requests_total[24h]))`
	trafficErrors   = `sum(increase(kong_http_requests_total{code=~"5.."}[24h]))`
	pageSelector    = `service=~"httproute.public-demo.status-web.*"`
	trafficPageP95  = `histogram_quantile(0.95, sum by (le) (increase(kong_request_latency_ms_bucket{` + pageSelector + `}[24h])))`
	trafficPageFast = `sum(increase(kong_request_latency_ms_bucket{` + pageSelector + `, le="250.0"}[24h]))` +
		` / sum(increase(kong_request_latency_ms_count{` + pageSelector + `}[24h]))`
	// The first scrape of Kong's metrics in the window: before 2026-11-21
	// there were none, so "the last 24 hours" may be shorter.
	trafficSince = `min_over_time(timestamp(sum(kong_http_requests_total))[24h:10m])`
)

type trafficInfo struct {
	Requests *float64 `json:"requests"`
	// Share of requests answered with a 5xx, 0..1.
	ErrorRatio *float64 `json:"errorRatio"`
	// The status page's own requests: 95th percentile latency in ms, and
	// the share under its 250 ms target.
	PageP95Ms     *float64 `json:"pageP95Ms"`
	PageFastRatio *float64 `json:"pageFastRatio"`
	Since         *int64   `json:"since"`
	WindowHours   int      `json:"windowHours"`
}

func (p *promClient) traffic(ctx context.Context) (*trafficInfo, error) {
	info := &trafficInfo{WindowHours: 24}
	reqs, err := p.query(ctx, trafficRequests)
	if err != nil {
		return nil, err
	}
	if len(reqs) > 0 {
		if v, ok := sampleValue(reqs[0].Value); ok {
			info.Requests = &v
			if errs := p.scalar(ctx, trafficErrors); errs != nil && v > 0 {
				r := *errs / v
				info.ErrorRatio = &r
			} else if v > 0 {
				zero := 0.0
				info.ErrorRatio = &zero
			}
		}
	}
	info.PageP95Ms = p.scalar(ctx, trafficPageP95)
	info.PageFastRatio = p.scalar(ctx, trafficPageFast)
	if since := p.scalar(ctx, trafficSince); since != nil {
		s := int64(*since)
		info.Since = &s
	}
	return info, nil
}

type trafficCache struct {
	prom *promClient

	mu      sync.Mutex
	last    *trafficInfo
	fetched time.Time
}

func (c *trafficCache) get(ctx context.Context) (*trafficInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last != nil && time.Since(c.fetched) < trafficCacheTTL {
		return c.last, nil
	}
	resp, err := c.prom.traffic(ctx)
	if err != nil {
		return nil, err
	}
	c.last, c.fetched = resp, time.Now()
	return resp, nil
}
