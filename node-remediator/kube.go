package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	annCordonedBy   = "smega.eu/cordoned-by"
	annReason       = "smega.eu/cordoned-reason"
	annCordonedAt   = "smega.eu/cordoned-at"
	annUncordonedAt = "smega.eu/auto-uncordoned-at"
	annOptOut       = "smega.eu/auto-remediation" // "off"
	me              = "node-remediator"
	saDir           = "/var/run/secrets/kubernetes.io/serviceaccount"
)

type objectMeta struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace,omitempty"`
	UID               string            `json:"uid,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
	DeletionTimestamp *string           `json:"deletionTimestamp,omitempty"`
	OwnerReferences   []struct {
		Kind string `json:"kind"`
	} `json:"ownerReferences,omitempty"`
}

type condition struct {
	Type               string    `json:"type"`
	Status             string    `json:"status"`
	Message            string    `json:"message"`
	LastTransitionTime time.Time `json:"lastTransitionTime"`
}

type node struct {
	Metadata objectMeta `json:"metadata"`
	Spec     struct {
		Unschedulable bool `json:"unschedulable"`
	} `json:"spec"`
	Status struct {
		Conditions []condition `json:"conditions"`
	} `json:"status"`
}

type pod struct {
	Metadata objectMeta `json:"metadata"`
	Spec     struct {
		Volumes []struct {
			PersistentVolumeClaim *struct {
				ClaimName string `json:"claimName"`
			} `json:"persistentVolumeClaim"`
		} `json:"volumes"`
	} `json:"spec"`
	Status struct {
		Phase string `json:"phase"`
	} `json:"status"`
}

type kube struct {
	base   string
	http   *http.Client
	header http.Header
}

func inCluster() (*kube, error) {
	token, err := os.ReadFile(saDir + "/token")
	if err != nil {
		return nil, err
	}
	ca, err := os.ReadFile(saDir + "/ca.crt")
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("no CA certificate in %s/ca.crt", saDir)
	}
	return &kube{
		base: "https://kubernetes.default.svc",
		http: &http.Client{
			Timeout:   10 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
		},
		header: http.Header{"Authorization": {"Bearer " + strings.TrimSpace(string(token))}},
	}, nil
}

// do sends one request and decodes a 2xx answer into out ( if not nil ).
// It returns the HTTP status, so callers can tell a PDB refusal ( 429 )
// from a failure.
func (k *kube) do(ctx context.Context, method, path, contentType string, body, out any) (int, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, k.base+path, r)
	if err != nil {
		return 0, err
	}
	for h, v := range k.header {
		req.Header[h] = v
	}
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := k.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return resp.StatusCode, fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, bytes.TrimSpace(msg))
	}
	if out == nil {
		return resp.StatusCode, nil
	}
	return resp.StatusCode, json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out)
}

func (k *kube) nodes(ctx context.Context) ([]node, error) {
	var l struct{ Items []node }
	_, err := k.do(ctx, http.MethodGet, "/api/v1/nodes", "", nil, &l)
	return l.Items, err
}

func (k *kube) podsOn(ctx context.Context, name string) ([]pod, error) {
	var l struct{ Items []pod }
	q := url.Values{"fieldSelector": {"spec.nodeName=" + name}}
	_, err := k.do(ctx, http.MethodGet, "/api/v1/pods?"+q.Encode(), "", nil, &l)
	return l.Items, err
}

func (k *kube) patchNode(ctx context.Context, name string, patch any) error {
	_, err := k.do(ctx, http.MethodPatch, "/api/v1/nodes/"+name, "application/merge-patch+json", patch, nil)
	return err
}

// evict asks for a pod's eviction, which honours PodDisruptionBudgets.
// done is false when a budget refused it for now ( try again next round ).
func (k *kube) evict(ctx context.Context, p pod) (done bool, err error) {
	ns, name := p.Metadata.Namespace, p.Metadata.Name
	body := map[string]any{
		"apiVersion": "policy/v1",
		"kind":       "Eviction",
		"metadata":   map[string]string{"name": name, "namespace": ns},
	}
	code, err := k.do(ctx, http.MethodPost, "/api/v1/namespaces/"+ns+"/pods/"+name+"/eviction", "application/json", body, nil)
	switch {
	case err == nil, code == http.StatusNotFound:
		return true, nil
	case code == http.StatusTooManyRequests:
		return false, nil
	}
	return false, err
}

// pinned says whether a pod's storage ties it to its node: a local-path
// volume ( Loki, Grafana, Alertmanager, Tempo here ) or a local PV
// ( Prometheus ). Evicted, it would wait Pending until the node is back,
// so it is better off running on a slow node than not at all.
func (k *kube) pinned(ctx context.Context, p pod) (bool, error) {
	for _, v := range p.Spec.Volumes {
		if v.PersistentVolumeClaim == nil {
			continue
		}
		var pvc struct {
			Spec struct {
				VolumeName string `json:"volumeName"`
			} `json:"spec"`
		}
		path := "/api/v1/namespaces/" + p.Metadata.Namespace + "/persistentvolumeclaims/" + v.PersistentVolumeClaim.ClaimName
		if _, err := k.do(ctx, http.MethodGet, path, "", nil, &pvc); err != nil {
			return false, err
		}
		if pvc.Spec.VolumeName == "" {
			continue
		}
		var pv struct {
			Spec struct {
				NodeAffinity json.RawMessage `json:"nodeAffinity"`
				Local        json.RawMessage `json:"local"`
				HostPath     json.RawMessage `json:"hostPath"`
			} `json:"spec"`
		}
		if _, err := k.do(ctx, http.MethodGet, "/api/v1/persistentvolumes/"+pvc.Spec.VolumeName, "", nil, &pv); err != nil {
			return false, err
		}
		if len(pv.Spec.NodeAffinity) > 0 || len(pv.Spec.Local) > 0 || len(pv.Spec.HostPath) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// event records what we did on the node, where kubectl describe node and
// alert-diagnostics see it.
func (k *kube) event(ctx context.Context, n node, kind, reason, msg string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	body := map[string]any{
		"apiVersion": "v1",
		"kind":       "Event",
		"metadata":   map[string]string{"generateName": n.Metadata.Name + ".", "namespace": "default"},
		"involvedObject": map[string]string{
			"kind": "Node", "name": n.Metadata.Name, "uid": n.Metadata.UID,
		},
		"type": kind, "reason": reason, "message": msg,
		"source":         map[string]string{"component": me},
		"firstTimestamp": now, "lastTimestamp": now, "count": 1,
	}
	_, err := k.do(ctx, http.MethodPost, "/api/v1/namespaces/default/events", "application/json", body, nil)
	return err
}

func view(n node, watched []string) nodeView {
	a := n.Metadata.Annotations
	v := nodeView{
		Name:          n.Metadata.Name,
		Unschedulable: n.Spec.Unschedulable,
		Ours:          a[annCordonedBy] == me,
		OptOut:        a[annOptOut] == "off",
	}
	_, v.ControlPlane = n.Metadata.Labels["node-role.kubernetes.io/control-plane"]
	if t, err := time.Parse(time.RFC3339, a[annUncordonedAt]); err == nil {
		v.UncordonedAt = t
	}
	seen, falses := 0, 0
	for _, c := range n.Status.Conditions {
		if c.Type == "Ready" {
			v.Ready = c.Status == "True"
			continue
		}
		for _, w := range watched {
			if c.Type != w {
				continue
			}
			seen++
			switch c.Status {
			case "True":
				if v.Problem == "" {
					v.Problem, v.Message, v.ProblemSince = c.Type, c.Message, c.LastTransitionTime
				}
			case "False":
				falses++
				if c.LastTransitionTime.After(v.HealthySince) {
					v.HealthySince = c.LastTransitionTime
				}
			}
		}
	}
	v.Healthy = seen > 0 && falses == seen
	return v
}

// evictable: what kubectl drain would move. DaemonSet pods stay ( they
// are meant to run everywhere ), static pods can't be evicted, finished
// pods don't run, and a pod nothing owns would just be gone.
func evictable(p pod) (bool, string) {
	switch {
	case p.Metadata.DeletionTimestamp != nil:
		return false, "terminating"
	case p.Status.Phase == "Succeeded" || p.Status.Phase == "Failed":
		return false, "finished"
	case p.Metadata.Annotations["kubernetes.io/config.mirror"] != "":
		return false, "static pod"
	case len(p.Metadata.OwnerReferences) == 0:
		return false, "no controller"
	}
	for _, o := range p.Metadata.OwnerReferences {
		if o.Kind == "DaemonSet" {
			return false, "daemonset"
		}
	}
	return true, ""
}
