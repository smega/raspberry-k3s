// alert-diagnostics: when an alert fires, collect what I would look at
// first and send it to my phone as a second, quiet ntfy message.
//
// Alertmanager still sends every alert straight to ntfy, as before; this
// service is an extra webhook on the same receivers. If it is down or
// slow, no alert is lost, only its diagnostics.
//
// For each firing alert ( deduplicated for an hour, so repeats don't
// resend it ):
//
//	events      the pod's ( or deployment's, node's, namespace's warning )
//	            Kubernetes events
//	logs        the last lines from Loki: the pod's, or errors in the namespace
//	last change the target's ( or namespace's ) last rollout, the git
//	            revision Flux fetched last, Kustomizations not ready
//	SLO         the status page's error budgets, and what else is firing
//
// Standard library only: the Kubernetes API, Prometheus and Loki are plain
// HTTP. Read-only everywhere ( deployments/alert-diagnostics.yaml ).
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type alert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    time.Time         `json:"startsAt"`
	Fingerprint string            `json:"fingerprint"`
}

type webhook struct {
	Alerts []alert `json:"alerts"`
}

const (
	dedupFor       = time.Hour
	collectTimeout = 25 * time.Second
)

type server struct {
	kube *kubeClient
	prom *httpJSON
	loki *httpJSON
	ntfy *ntfyClient

	mu   sync.Mutex
	sent map[string]time.Time
}

// fresh reports whether this alert's diagnostics haven't been sent within
// dedupFor, and marks them as sent.
func (s *server) fresh(fp string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, t := range s.sent {
		if now.Sub(t) > dedupFor {
			delete(s.sent, k)
		}
	}
	if _, ok := s.sent[fp]; ok {
		return false
	}
	s.sent[fp] = now
	return true
}

func (s *server) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var hook webhook
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&hook); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	// Answer Alertmanager at once: collecting takes seconds, and it would
	// retry ( and resend ) on a timeout.
	w.WriteHeader(http.StatusAccepted)
	now := time.Now()
	for _, a := range hook.Alerts {
		if a.Status != "firing" || !s.fresh(a.Fingerprint, now) {
			continue
		}
		go s.diagnose(a)
	}
}

func (s *server) diagnose(a alert) {
	ctx, cancel := context.WithTimeout(context.Background(), collectTimeout)
	defer cancel()
	name := a.Labels["alertname"]
	body := s.report(ctx, a)
	if err := s.ntfy.send(ctx, "Diagnostics: "+name, body, a.Annotations["runbook_url"]); err != nil {
		log.Printf("%s: could not send to ntfy: %v", name, err)
		return
	}
	log.Printf("%s: diagnostics sent ( %d bytes )", name, len(body))
}

func main() {
	topic, err := ntfyTopic(os.Getenv("NTFY_URL_FILE"))
	if err != nil {
		log.Fatalf("ntfy topic: %v", err)
	}
	kube, err := inCluster()
	if err != nil {
		log.Fatalf("kubernetes: %v", err)
	}
	httpc := &http.Client{Timeout: 10 * time.Second}
	s := &server{
		kube: kube,
		prom: &httpJSON{base: strings.TrimRight(os.Getenv("PROMETHEUS_URL"), "/"), http: httpc},
		loki: &httpJSON{base: strings.TrimRight(os.Getenv("LOKI_URL"), "/"), http: httpc},
		ntfy: &ntfyClient{topic: topic, http: httpc},
		sent: map[string]time.Time{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/alert", s.handle)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok\n")) })
	log.Printf("alert-diagnostics listening on :8080")
	srv := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
