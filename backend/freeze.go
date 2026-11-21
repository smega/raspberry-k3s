package main

import (
	"context"
	"sort"
	"sync"
	"time"
)

// The error budget policy, for the status page: is the DeployFreeze alert
// firing ( deployments/15-deploy-freeze.yaml ), since when, and how much
// budget the services it watches have left. This pod has no Kubernetes
// token, so it reports the policy's decision from Prometheus; the
// deploy-freeze CronJob carries it out within 5 minutes. Fixed queries
// only, like nodes.go.

const (
	freezeCacheTTL = 30 * time.Second
	freezeFiring   = `ALERTS{alertname="DeployFreeze",alertstate="firing"}`
	freezeSince    = `min(ALERTS_FOR_STATE{alertname="DeployFreeze"})`
	freezeBudgets  = `min by (service) (status_service_error_budget_remaining_ratio{service=~"Cluster dashboard|Whoami demo"})`
)

type freezeBudget struct {
	Service string `json:"service"`
	// Share of the 30-day error budget left, 0..1 ( below 0 when overspent ).
	Remaining float64 `json:"remaining"`
}

type freezeInfo struct {
	Frozen bool `json:"frozen"`
	// The services whose spent budget froze the deploys.
	Services []string `json:"services"`
	Since    *int64   `json:"since"`
	// Every service the policy watches, frozen or not.
	Budgets []freezeBudget `json:"budgets"`
}

func (p *promClient) freeze(ctx context.Context) (*freezeInfo, error) {
	firing, err := p.query(ctx, freezeFiring)
	if err != nil {
		return nil, err
	}
	info := &freezeInfo{Services: []string{}, Budgets: []freezeBudget{}}
	for _, s := range firing {
		info.Services = append(info.Services, s.Metric["service"])
	}
	sort.Strings(info.Services)
	if info.Frozen = len(info.Services) > 0; info.Frozen {
		if v := p.scalar(ctx, freezeSince); v != nil {
			since := int64(*v)
			info.Since = &since
		}
	}

	budgets, err := p.query(ctx, freezeBudgets)
	if err != nil {
		return nil, err
	}
	for _, s := range budgets {
		if v, ok := sampleValue(s.Value); ok {
			info.Budgets = append(info.Budgets, freezeBudget{s.Metric["service"], v})
		}
	}
	sort.Slice(info.Budgets, func(i, j int) bool { return info.Budgets[i].Service < info.Budgets[j].Service })
	return info, nil
}

type freezeCache struct {
	prom *promClient

	mu      sync.Mutex
	last    *freezeInfo
	fetched time.Time
}

func (c *freezeCache) get(ctx context.Context) (*freezeInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last != nil && time.Since(c.fetched) < freezeCacheTTL {
		return c.last, nil
	}
	resp, err := c.prom.freeze(ctx)
	if err != nil {
		return nil, err
	}
	c.last, c.fetched = resp, time.Now()
	return resp, nil
}
