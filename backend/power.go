package main

import (
	"context"
	"net/url"
	"strconv"
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
	powerCurrent  = `max(homeassistant_sensor_current_a{job="home-assistant"})`
	// The plug reports whole hundredths of an amp, so the raw line only
	// jumps between two or three values; the chart uses 15-minute averages.
	powerCurrentSmooth = `max(avg_over_time(homeassistant_sensor_current_a{job="home-assistant"}[15m]))`
	// Real power over apparent power ( volts x amps ): USB power supplies
	// draw current in short pulses, so the amps are about twice what the
	// watts suggest.
	powerFactor = `cluster:power_watts / (max(homeassistant_sensor_voltage_v{job="home-assistant"}) * ` + powerCurrent + `)`
	// The first reading in the window: the plug went in on 2026-11-24, so
	// "the last 24 hours" may be shorter.
	powerSince = `min_over_time(timestamp(cluster:power_watts)[24h:5m])`
	// Unit rate of the home's electricity tariff, GBP per kWh ( 26.32p,
	// 2026-11-24 ). Standing charge left out: it's paid with or without
	// the cluster.
	powerTariffGBP = 0.2632

	// The charts: the last day in 5-minute points, and the 24-hour average
	// over the last week in hourly points.
	powerDayStep  = 5 * time.Minute
	powerWeek     = 7 * 24 * time.Hour
	powerWeekStep = time.Hour
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
	CurrentA    *float64 `json:"currentA"`
	PowerFactor *float64 `json:"powerFactor"`

	WattsHistory   []point `json:"wattsHistory"`
	AvgHistory     []point `json:"avgHistory"`
	CurrentHistory []point `json:"currentHistory"`
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
	info.CurrentA = p.scalar(ctx, powerCurrent)
	info.PowerFactor = p.scalar(ctx, powerFactor)

	end := time.Now()
	info.WattsHistory = p.series(ctx, powerNow, end, historyWindow, powerDayStep, round1)
	info.AvgHistory = p.series(ctx, powerAvg24h, end, powerWeek, powerWeekStep, round1)
	info.CurrentHistory = p.series(ctx, powerCurrentSmooth, end, historyWindow, powerDayStep, round3)
	return info, nil
}

// series runs a range query for one series. Failures only cost the chart,
// not the numbers above it.
func (p *promClient) series(ctx context.Context, q string, end time.Time, window, step time.Duration, round func(float64) float64) []point {
	res, err := p.get(ctx, "/api/v1/query_range", url.Values{
		"query": {q},
		"start": {strconv.FormatInt(end.Add(-window).Unix(), 10)},
		"end":   {strconv.FormatInt(end.Unix(), 10)},
		"step":  {strconv.Itoa(int(step.Seconds()))},
	})
	if err != nil || len(res) == 0 {
		return nil
	}
	pts := make([]point, 0, len(res[0].Values))
	for _, pair := range res[0].Values {
		t, okT := sampleTime(pair)
		v, okV := sampleValue(pair)
		if okT && okV {
			pts = append(pts, point{T: t, V: round(v)})
		}
	}
	return pts
}

func round3(v float64) float64 {
	return float64(int64(v*1000+0.5)) / 1000
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
