package main

import (
	"context"
	"sync"
	"time"
)

// What the whole cluster draws from the wall, from a metering smart plug in
// front of the USB hub that feeds all four Pis ( monitoring/home-power-rules.yaml ).
// Fixed queries only, like traffic.go.

const (
	powerCacheTTL = time.Minute
	powerNow      = `cluster:power_watts`
	powerAvg24h   = `avg_over_time(cluster:power_watts[24h])`
	powerMax24h   = `max_over_time(cluster:power_watts[24h])`
	powerMonthKWh = `cluster:energy_kwh:30d_projected`
	// The first reading in the window: the plug went in on 2026-11-24, so
	// "the last 24 hours" may be shorter.
	powerSince = `min_over_time(timestamp(cluster:power_watts)[24h:5m])`
	// Unit rate of the home's electricity tariff, GBP per kWh ( 26.32p,
	// 2026-11-24 ). Standing charge left out: it's paid with or without
	// the cluster.
	powerTariffGBP = 0.2632
)

type powerInfo struct {
	Watts       *float64 `json:"watts"`
	AvgWatts    *float64 `json:"avgWatts"`
	MaxWatts    *float64 `json:"maxWatts"`
	MonthKWh    *float64 `json:"monthKWh"`
	MonthCost   *float64 `json:"monthCostGBP"`
	TariffGBP   float64  `json:"tariffGBPPerKWh"`
	Since       *int64   `json:"since"`
	WindowHours int      `json:"windowHours"`
}

func (p *promClient) power(ctx context.Context) (*powerInfo, error) {
	info := &powerInfo{WindowHours: 24, TariffGBP: powerTariffGBP}
	now, err := p.query(ctx, powerNow)
	if err != nil {
		return nil, err
	}
	if len(now) > 0 {
		if v, ok := sampleValue(now[0].Value); ok {
			info.Watts = &v
		}
	}
	info.AvgWatts = p.scalar(ctx, powerAvg24h)
	info.MaxWatts = p.scalar(ctx, powerMax24h)
	info.MonthKWh = p.scalar(ctx, powerMonthKWh)
	if info.MonthKWh != nil {
		c := *info.MonthKWh * powerTariffGBP
		info.MonthCost = &c
	}
	if since := p.scalar(ctx, powerSince); since != nil {
		s := int64(*since)
		info.Since = &s
	}
	return info, nil
}

type powerCache struct {
	prom *promClient

	mu      sync.Mutex
	last    *powerInfo
	fetched time.Time
}

func (c *powerCache) get(ctx context.Context) (*powerInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last != nil && time.Since(c.fetched) < powerCacheTTL {
		return c.last, nil
	}
	resp, err := c.prom.power(ctx)
	if err != nil {
		return nil, err
	}
	c.last, c.fetched = resp, time.Now()
	return resp, nil
}
