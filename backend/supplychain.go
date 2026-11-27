package main

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// What runs here and where it came from: every own image running in the
// cluster ( registry.smdoes.eu/raspberry/** ), by digest, with what Trivy
// found fixable in it. Fixed queries only, like traffic.go.
//
// The signature, SBOM and SLSA provenance aren't read here: the
// policy-controller checks all three at admission in the namespaces it
// watches ( image-policies/ ), so "running in a watched namespace" means
// "verified". The page links each digest to the public transparency log,
// where anyone can check it again.

const (
	supplyCacheTTL = 5 * time.Minute
	ownRegistry    = "registry.smdoes.eu/raspberry/"
	// kube-state-metrics: one series per running container, with the
	// resolved digest in image_id.
	supplyRunning = `max by (namespace, container, image_spec, image_id) (kube_pod_container_info{image_spec=~"registry.smdoes.eu/raspberry/.*"})`
	// Trivy Operator reports fixable HIGH and CRITICAL only
	// ( trivy-operator/values.yaml ), one series per workload and severity.
	supplyVulns      = `max by (image_digest, severity) (trivy_image_vulnerabilities{image_registry="registry.smdoes.eu",image_repository=~"raspberry/.*"})`
	supplyThirdParty = `max by (image_registry, image_repository, image_tag, severity) (trivy_image_vulnerabilities{image_repository!~"raspberry/.*|raspberry-preview/.*"})`
)

// The namespaces labelled policy.sigstore.dev/include=true.
var verifiedNamespaces = map[string]bool{"public-demo": true, "public-dashboard": true, "cpu-led": true}

// main-<unix build time>-<7 hex of the commit>, from build-image.yml.
var ciTag = regexp.MustCompile(`^main-([0-9]+)-([a-f0-9]{7})$`)

type supplyImage struct {
	Name       string   `json:"name"`
	Tag        string   `json:"tag"`
	Digest     string   `json:"digest"`
	BuiltAt    *int64   `json:"builtAt"`
	Commit     string   `json:"commit"`
	Repo       string   `json:"repo"`
	Namespaces []string `json:"namespaces"`
	Verified   bool     `json:"verifiedAtAdmission"`
	Critical   int      `json:"critical"`
	High       int      `json:"high"`
}

type supplyInfo struct {
	Images     []supplyImage `json:"images"`
	ThirdParty struct {
		Images        int `json:"images"`
		WithCritical  int `json:"withCritical"`
		WithHigh      int `json:"withHigh"`
		CriticalTotal int `json:"criticalTotal"`
		HighTotal     int `json:"highTotal"`
	} `json:"thirdParty"`
	GeneratedAt int64 `json:"generatedAt"`
}

// sourceRepo: the dashboard's images are built from their own repo with
// the same CI workflow.
func sourceRepo(name string) string {
	if strings.HasPrefix(name, "dashboard-") {
		return "smega/cluster-dashboard"
	}
	return "smega/raspberry-k3s"
}

func (p *promClient) supplyChain(ctx context.Context) (*supplyInfo, error) {
	running, err := p.query(ctx, supplyRunning)
	if err != nil {
		return nil, err
	}
	byDigest := map[string]*supplyImage{}
	for _, s := range running {
		ref := strings.TrimPrefix(s.Metric["image_spec"], ownRegistry)
		at := strings.LastIndex(s.Metric["image_id"], "@")
		if at < 0 {
			continue
		}
		digest := s.Metric["image_id"][at+1:]
		ref, _, _ = strings.Cut(ref, "@")
		name, tag, _ := strings.Cut(ref, ":")
		img := byDigest[digest]
		if img == nil {
			img = &supplyImage{Name: name, Tag: tag, Digest: digest, Repo: sourceRepo(name), Verified: true}
			if m := ciTag.FindStringSubmatch(tag); m != nil {
				if ts, err := strconv.ParseInt(m[1], 10, 64); err == nil {
					img.BuiltAt = &ts
				}
				img.Commit = m[2]
			}
			byDigest[digest] = img
		}
		ns := s.Metric["namespace"]
		if !contains(img.Namespaces, ns) {
			img.Namespaces = append(img.Namespaces, ns)
		}
		// Verified only if every place it runs checks it.
		if !verifiedNamespaces[ns] {
			img.Verified = false
		}
	}

	// Without Trivy's numbers the list is still worth showing.
	if vulns, err := p.query(ctx, supplyVulns); err == nil {
		for _, s := range vulns {
			img := byDigest[s.Metric["image_digest"]]
			v, ok := sampleValue(s.Value)
			if img == nil || !ok {
				continue
			}
			switch s.Metric["severity"] {
			case "Critical":
				img.Critical = int(v)
			case "High":
				img.High = int(v)
			}
		}
	}

	info := &supplyInfo{GeneratedAt: time.Now().Unix()}
	for _, img := range byDigest {
		sort.Strings(img.Namespaces)
		info.Images = append(info.Images, *img)
	}
	sort.Slice(info.Images, func(i, j int) bool {
		if info.Images[i].Name != info.Images[j].Name {
			return info.Images[i].Name < info.Images[j].Name
		}
		return info.Images[i].Tag > info.Images[j].Tag
	})

	if third, err := p.query(ctx, supplyThirdParty); err == nil {
		type counts struct{ c, h int }
		per := map[string]*counts{}
		for _, s := range third {
			key := s.Metric["image_registry"] + "/" + s.Metric["image_repository"] + ":" + s.Metric["image_tag"]
			v, ok := sampleValue(s.Value)
			if !ok {
				continue
			}
			c := per[key]
			if c == nil {
				c = &counts{}
				per[key] = c
			}
			switch s.Metric["severity"] {
			case "Critical":
				c.c = int(v)
			case "High":
				c.h = int(v)
			}
		}
		t := &info.ThirdParty
		for _, c := range per {
			t.Images++
			t.CriticalTotal += c.c
			t.HighTotal += c.h
			if c.c > 0 {
				t.WithCritical++
			}
			if c.h > 0 {
				t.WithHigh++
			}
		}
	}
	return info, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

type supplyCache struct {
	prom *promClient

	mu      sync.Mutex
	last    *supplyInfo
	fetched time.Time
}

func (c *supplyCache) get(ctx context.Context) (*supplyInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last != nil && time.Since(c.fetched) < supplyCacheTTL {
		return c.last, nil
	}
	resp, err := c.prom.supplyChain(ctx)
	if err != nil {
		return nil, err
	}
	c.last, c.fetched = resp, time.Now()
	return resp, nil
}
