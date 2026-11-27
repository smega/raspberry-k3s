package main

import (
	"fmt"
	"sort"
	"time"
)

// What the remediator knows about one node, read from the API each round.
type nodeView struct {
	Name          string
	ControlPlane  bool
	Ready         bool
	Unschedulable bool
	// Cordoned by us: our annotation is on it. Only these are uncordoned.
	Ours   bool
	OptOut bool
	// The first watched condition that is True ( "" if none ), its
	// message, and since when.
	Problem      string
	Message      string
	ProblemSince time.Time
	// Every watched condition the node has is False. HealthySince is the
	// latest of their transitions. A node with none of them ( Node Problem
	// Detector not running there ) is neither healthy nor sick: no data.
	Healthy      bool
	HealthySince time.Time
	// When we last uncordoned it ourselves.
	UncordonedAt time.Time
}

type config struct {
	Conditions []string
	// How long a condition must be True before we act. The detector's own
	// check already needs three slow rounds in a row ( 15 minutes ).
	ActAfter time.Duration
	// How long every condition must be False again before we uncordon.
	UncordonAfter time.Duration
	// After we uncordon a node, it isn't cordoned again automatically for
	// this long: a card on the edge would otherwise move every pod back and
	// forth all day. NodeProblemNotRemediated tells a human instead.
	Cooldown time.Duration
	// Healthy, schedulable workers that must be left besides the one we
	// cordon. Three workers here: cordoning one leaves two.
	MinHealthyOthers int
}

type plan struct {
	Cordon   string // at most one node per round
	Reason   string
	Uncordon []string
	// Our annotation is on a node someone else has uncordoned by hand:
	// their decision stands, we only drop the annotation.
	Forget []string
	// Nodes with a problem that we leave alone, and why.
	Blocked map[string]string
}

func decide(nodes []nodeView, now time.Time, cfg config) plan {
	p := plan{Blocked: map[string]string{}}

	for _, n := range nodes {
		switch {
		case n.Ours && !n.Unschedulable:
			p.Forget = append(p.Forget, n.Name)
		case n.Ours && n.Healthy && n.Ready && now.Sub(n.HealthySince) >= cfg.UncordonAfter:
			p.Uncordon = append(p.Uncordon, n.Name)
		}
	}

	var candidates []nodeView
	for _, n := range nodes {
		if n.Problem == "" || n.Ours {
			continue
		}
		switch {
		case n.ControlPlane:
			p.Blocked[n.Name] = "control plane, never drained automatically"
		case n.OptOut:
			p.Blocked[n.Name] = "auto-remediation turned off on this node"
		case n.Unschedulable:
			p.Blocked[n.Name] = "already cordoned by someone else"
		case now.Sub(n.ProblemSince) < cfg.ActAfter:
			p.Blocked[n.Name] = "problem too recent"
		case !n.UncordonedAt.IsZero() && now.Sub(n.UncordonedAt) < cfg.Cooldown:
			p.Blocked[n.Name] = "uncordoned automatically less than " + cfg.Cooldown.String() + " ago"
		default:
			candidates = append(candidates, n)
		}
	}
	if len(candidates) == 0 {
		return p
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].ProblemSince.Equal(candidates[j].ProblemSince) {
			return candidates[i].ProblemSince.Before(candidates[j].ProblemSince)
		}
		return candidates[i].Name < candidates[j].Name
	})

	c := candidates[0]
	for _, n := range candidates[1:] {
		p.Blocked[n.Name] = "one node at a time"
	}
	if why := guard(nodes, c, cfg); why != "" {
		p.Blocked[c.Name] = why
		return p
	}
	p.Cordon = c.Name
	p.Reason = fmt.Sprintf("%s: %s", c.Problem, c.Message)
	return p
}

// guard says why cordoning c now would be unsafe, or "".
func guard(nodes []nodeView, c nodeView, cfg config) string {
	healthy := 0
	for _, n := range nodes {
		if n.Name == c.Name {
			continue
		}
		if n.Unschedulable {
			return n.Name + " is cordoned, one node at a time"
		}
		if !n.Ready {
			return n.Name + " is not Ready"
		}
		if !n.ControlPlane && n.Problem == "" {
			healthy++
		}
	}
	if healthy < cfg.MinHealthyOthers {
		return fmt.Sprintf("only %d healthy workers would be left, need %d", healthy, cfg.MinHealthyOthers)
	}
	return ""
}
