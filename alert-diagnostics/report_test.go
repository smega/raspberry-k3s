package main

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestLogQuery(t *testing.T) {
	cases := []struct {
		t    target
		want string
	}{
		{target{}, ""},
		{target{namespace: "public-demo", pod: "status-api-7d9-x"}, `{namespace="public-demo", pod="status-api-7d9-x"}`},
		{target{namespace: "public-demo", workload: "status.web"}, `{namespace="public-demo", pod=~"status\\.web-.*"}`},
		{target{namespace: "kong"}, `{namespace="kong"} |~ "(?i)(error|fail|panic|fatal|refused|timeout)"`},
	}
	for _, c := range cases {
		if got := logQuery(c.t); got != c.want {
			t.Errorf("logQuery(%+v) = %s, want %s", c.t, got, c.want)
		}
	}
}

func TestMatchEvents(t *testing.T) {
	ev := func(typ, name, ts string) kubeEvent {
		e := kubeEvent{Type: typ, LastTimestamp: ts}
		e.InvolvedObject.Name = name
		return e
	}
	all := []kubeEvent{
		ev("Normal", "status-web-abc-1", "2026-11-21T10:00:00Z"),
		ev("Warning", "status-web-abc-1", "2026-11-21T10:05:00Z"),
		ev("Warning", "status-webhook", "2026-11-21T10:06:00Z"),
		ev("Normal", "whoami-1", "2026-11-21T10:07:00Z"),
	}
	got := matchEvents(all, target{workload: "status-web"})
	if len(got) != 2 || got[0].LastTimestamp != "2026-11-21T10:05:00Z" {
		t.Errorf("workload: got %+v", got)
	}
	got = matchEvents(all, target{namespace: "public-demo"})
	if len(got) != 2 || got[0].InvolvedObject.Name != "status-webhook" {
		t.Errorf("namespace warnings: got %+v", got)
	}
}

func TestTruncate(t *testing.T) {
	s := strings.Repeat("árvíztűrő\n", 600)
	got := truncate(s, ntfyMax)
	if len(got) > ntfyMax || !utf8.ValidString(got) || !strings.HasSuffix(got, "\n…") {
		t.Errorf("truncate: %d bytes, valid %v", len(got), utf8.ValidString(got))
	}
	if truncate("short", ntfyMax) != "short" {
		t.Error("short text changed")
	}
}

func TestShorts(t *testing.T) {
	if got := shortImage("registry.smdoes.eu/raspberry/status-web:main-1-abc1234@sha256:ff"); got != "status-web:main-1-abc1234" {
		t.Errorf("shortImage: %s", got)
	}
	if got := shortRevision("main@sha1:0123456789abcdef"); got != "main@0123456" {
		t.Errorf("shortRevision: %s", got)
	}
}

func TestFresh(t *testing.T) {
	s := &server{sent: map[string]time.Time{}}
	now := time.Now()
	if !s.fresh("a", now) || s.fresh("a", now.Add(time.Minute)) || !s.fresh("a", now.Add(2*time.Hour)) {
		t.Error("dedup window wrong")
	}
}
