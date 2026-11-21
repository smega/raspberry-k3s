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

// httpJSON is a GET-and-decode client for Prometheus, Loki and the
// Kubernetes API.
type httpJSON struct {
	base   string
	http   *http.Client
	header http.Header
}

func (c *httpJSON) get(ctx context.Context, path string, params url.Values, out any) error {
	u := c.base + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	for k, v := range c.header {
		req.Header[k] = v
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

type kubeClient struct{ httpJSON }

const saDir = "/var/run/secrets/kubernetes.io/serviceaccount"

func inCluster() (*kubeClient, error) {
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
	return &kubeClient{httpJSON{
		base: "https://kubernetes.default.svc",
		http: &http.Client{
			Timeout:   10 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
		},
		header: http.Header{"Authorization": {"Bearer " + strings.TrimSpace(string(token))}},
	}}, nil
}

// ntfyTopic reads the topic URL from the file Alertmanager's own ntfy
// webhook uses ( https://ntfy.sh/<topic>?template=... ) and drops the
// query: this service sends plain text.
func ntfyTopic(file string) (string, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(strings.TrimSpace(string(raw)))
	if err != nil || u.Scheme != "https" || u.Host == "" || len(u.Path) < 2 {
		return "", fmt.Errorf("%s is not an ntfy topic URL", file)
	}
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

type ntfyClient struct {
	topic string
	http  *http.Client
}

// ntfy rejects messages over 4096 bytes.
const ntfyMax = 3900

func (n *ntfyClient) send(ctx context.Context, title, body, click string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.topic, bytes.NewBufferString(truncate(body, ntfyMax)))
	if err != nil {
		return err
	}
	req.Header.Set("Title", title)
	// Low: no sound or vibration. The alert itself already rang.
	req.Header.Set("Priority", "2")
	req.Header.Set("Tags", "mag")
	if strings.HasPrefix(click, "https://") {
		req.Header.Set("Click", click)
	}
	resp, err := n.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ntfy: HTTP %d", resp.StatusCode)
	}
	return nil
}

// truncate cuts s to at most max bytes on a line boundary where it can,
// never inside a UTF-8 character.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	const more = "\n…"
	cut := max - len(more)
	for cut > 0 && cut < len(s) && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	if nl := strings.LastIndexByte(s[:cut], '\n'); nl > cut/2 {
		cut = nl
	}
	return s[:cut] + more
}
