package main

import (
	"bytes"
	"compress/gzip"
	"os"
	"strings"
	"sync"
	"time"
)

// The checker's data file (~200 KB) as it was last read, plain and
// gzipped. Reading and sending it whole on every request capped
// /api/status at ~100 req/s within its CPU limit (loadtest/, 2026-11-19);
// now a request costs a stat, and a client that accepts gzip gets ~16 KB.
// It's read again only when the file changes (the kubelet swaps the
// mounted ConfigMap, the checker writes every 5 minutes).
type statusFile struct {
	path string

	mu      sync.Mutex
	modTime time.Time
	size    int64
	plain   []byte
	gzipped []byte
}

func (f *statusFile) get() (plain, gzipped []byte, err error) {
	st, err := os.Stat(f.path)
	if err != nil {
		return nil, nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.plain != nil && st.ModTime().Equal(f.modTime) && st.Size() == f.size {
		return f.plain, f.gzipped, nil
	}
	b, err := os.ReadFile(f.path)
	if err != nil {
		return nil, nil, err
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	zw.Write(b)
	zw.Close()
	f.plain, f.gzipped, f.modTime, f.size = b, buf.Bytes(), st.ModTime(), st.Size()
	return f.plain, f.gzipped, nil
}

func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		enc, q, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.TrimSpace(enc) == "gzip" && strings.TrimSpace(q) != "q=0" {
			return true
		}
	}
	return false
}
