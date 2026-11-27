package main

import (
	"encoding/json"
	"testing"
	"time"
)

var (
	t0  = time.Date(2026, 11, 26, 6, 0, 0, 0, time.UTC)
	cfg = config{
		Conditions:       []string{"SDCardSlow", "ReadonlyFilesystem"},
		ActAfter:         2 * time.Minute,
		UncordonAfter:    15 * time.Minute,
		Cooldown:         6 * time.Hour,
		MinHealthyOthers: 2,
	}
)

// The cluster here: a control plane and three workers, all well.
func cluster() []nodeView {
	h := func(name string) nodeView {
		return nodeView{Name: name, Ready: true, Healthy: true, HealthySince: t0.Add(-48 * time.Hour)}
	}
	m := h("kube-master")
	m.ControlPlane = true
	return []nodeView{m, h("kube-slave1"), h("kube-slave2"), h("kube-slave3")}
}

func sick(n *nodeView, since time.Time) {
	n.Problem, n.Message, n.ProblemSince = "SDCardSlow", "synced 4 KB write 900 ms", since
	n.Healthy = false
}

func TestCordonsASickWorker(t *testing.T) {
	c := cluster()
	sick(&c[2], t0.Add(-5*time.Minute))
	p := decide(c, t0, cfg)
	if p.Cordon != "kube-slave2" {
		t.Fatalf("cordon = %q, want kube-slave2 ( blocked: %v )", p.Cordon, p.Blocked)
	}
}

func TestWaitsUntilActAfter(t *testing.T) {
	c := cluster()
	sick(&c[2], t0.Add(-1*time.Minute))
	if p := decide(c, t0, cfg); p.Cordon != "" || p.Blocked["kube-slave2"] != "problem too recent" {
		t.Fatalf("got %+v", p)
	}
}

func TestNeverTheControlPlane(t *testing.T) {
	c := cluster()
	sick(&c[0], t0.Add(-time.Hour))
	if p := decide(c, t0, cfg); p.Cordon != "" || p.Blocked["kube-master"] == "" {
		t.Fatalf("got %+v", p)
	}
}

func TestOneNodeAtATime(t *testing.T) {
	c := cluster()
	c[1].Unschedulable = true // kured, an upgrade, or us
	sick(&c[2], t0.Add(-time.Hour))
	if p := decide(c, t0, cfg); p.Cordon != "" {
		t.Fatalf("cordoned %s while kube-slave1 was cordoned", p.Cordon)
	}
}

func TestTwoSickOnlyTheFirst(t *testing.T) {
	c := cluster()
	sick(&c[2], t0.Add(-time.Hour))
	sick(&c[3], t0.Add(-2*time.Hour))
	p := decide(c, t0, cfg)
	// Two sick workers leave one healthy: not enough, so neither.
	if p.Cordon != "" || p.Blocked["kube-slave3"] == "" || p.Blocked["kube-slave2"] != "one node at a time" {
		t.Fatalf("got %+v", p)
	}
}

func TestNotWhenAnotherNodeIsDown(t *testing.T) {
	c := cluster()
	c[3].Ready = false
	sick(&c[2], t0.Add(-time.Hour))
	if p := decide(c, t0, cfg); p.Cordon != "" {
		t.Fatalf("cordoned %s with kube-slave3 NotReady", p.Cordon)
	}
}

func TestOptOut(t *testing.T) {
	c := cluster()
	c[2].OptOut = true
	sick(&c[2], t0.Add(-time.Hour))
	if p := decide(c, t0, cfg); p.Cordon != "" {
		t.Fatalf("cordoned an opted-out node")
	}
}

func TestCooldownAfterUncordon(t *testing.T) {
	c := cluster()
	sick(&c[2], t0.Add(-time.Hour))
	c[2].UncordonedAt = t0.Add(-2 * time.Hour)
	if p := decide(c, t0, cfg); p.Cordon != "" {
		t.Fatalf("cordoned again within the cooldown")
	}
	c[2].UncordonedAt = t0.Add(-7 * time.Hour)
	if p := decide(c, t0, cfg); p.Cordon != "kube-slave2" {
		t.Fatalf("not cordoned after the cooldown: %+v", p)
	}
}

func TestUncordonsWhenHealthyLongEnough(t *testing.T) {
	c := cluster()
	c[2].Ours, c[2].Unschedulable = true, true
	c[2].HealthySince = t0.Add(-10 * time.Minute)
	if p := decide(c, t0, cfg); len(p.Uncordon) != 0 {
		t.Fatalf("uncordoned after 10 minutes")
	}
	c[2].HealthySince = t0.Add(-20 * time.Minute)
	if p := decide(c, t0, cfg); len(p.Uncordon) != 1 {
		t.Fatalf("not uncordoned after 20 minutes: %+v", p)
	}
}

func TestKeepsCordonWithoutData(t *testing.T) {
	c := cluster()
	c[2].Ours, c[2].Unschedulable = true, true
	c[2].Healthy = false // the detector is gone: no conditions at all
	if p := decide(c, t0, cfg); len(p.Uncordon) != 0 {
		t.Fatalf("uncordoned with no data")
	}
}

func TestHandUncordonIsRespected(t *testing.T) {
	c := cluster()
	c[2].Ours = true // our annotation, but someone uncordoned it
	sick(&c[2], t0.Add(-time.Hour))
	p := decide(c, t0, cfg)
	if len(p.Forget) != 1 || p.Cordon != "" {
		t.Fatalf("got %+v", p)
	}
}

func TestView(t *testing.T) {
	var n node
	raw := `{"metadata":{"name":"kube-slave2","annotations":{"smega.eu/cordoned-by":"node-remediator"}},
	  "spec":{"unschedulable":true},
	  "status":{"conditions":[
	    {"type":"Ready","status":"True","lastTransitionTime":"2026-11-26T05:00:00Z"},
	    {"type":"SDCardSlow","status":"True","message":"slow","lastTransitionTime":"2026-11-26T05:30:00Z"},
	    {"type":"ReadonlyFilesystem","status":"False","lastTransitionTime":"2026-11-20T00:00:00Z"}]}}`
	if err := json.Unmarshal([]byte(raw), &n); err != nil {
		t.Fatal(err)
	}
	v := view(n, cfg.Conditions)
	if !v.Ready || !v.Ours || !v.Unschedulable || v.Problem != "SDCardSlow" || v.Healthy || v.ControlPlane {
		t.Fatalf("got %+v", v)
	}
}

func TestEvictable(t *testing.T) {
	ds := pod{}
	ds.Metadata.OwnerReferences = append(ds.Metadata.OwnerReferences, struct {
		Kind string `json:"kind"`
	}{"DaemonSet"})
	rs := pod{}
	rs.Metadata.OwnerReferences = append(rs.Metadata.OwnerReferences, struct {
		Kind string `json:"kind"`
	}{"ReplicaSet"})
	bare := pod{}
	for _, tc := range []struct {
		p    pod
		want bool
	}{{ds, false}, {rs, true}, {bare, false}} {
		if got, why := evictable(tc.p); got != tc.want {
			t.Errorf("evictable = %v ( %s ), want %v", got, why, tc.want)
		}
	}
}
