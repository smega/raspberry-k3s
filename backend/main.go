package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func main() {
	dataFile := os.Getenv("STATUS_DATA_FILE")
	if dataFile == "" {
		dataFile = "/data/status-data.json"
	}

	router := gin.Default()

	router.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET,OPTIONS")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	})

	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"ok":   true,
			"time": time.Now().UTC().Format(time.RFC3339),
		})
	})

	// The status-checker CronJob owns the data shape entirely (uptime%,
	// history, per-service checks) -- this handler just serves whatever
	// JSON it last wrote, byte for byte, rather than re-parsing it into a
	// Go struct the two services would have to keep in sync.
	router.GET("/api/status", func(c *gin.Context) {
		b, err := os.ReadFile(dataFile)
		if err != nil {
			log.Printf("could not read data file %s: %v", dataFile, err)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "status data not available yet"})
			return
		}
		c.Data(http.StatusOK, "application/json", b)
	})

	router.GET("/badge/:file", func(c *gin.Context) {
		slug, ok := strings.CutSuffix(c.Param("file"), ".svg")
		if !ok {
			c.Status(http.StatusNotFound)
			return
		}
		svc, err := findBadgeService(dataFile, slug)
		if err != nil {
			log.Printf("badge %s: %v", slug, err)
		}
		if err == nil && svc == nil {
			c.Status(http.StatusNotFound)
			return
		}
		value, color := badgeFor(svc)
		// Short cache: GitHub's image proxy and browsers re-fetch every few
		// minutes, matching the 5-minute check cadence.
		c.Header("Cache-Control", "public, max-age=300")
		c.Data(http.StatusOK, "image/svg+xml; charset=utf-8", []byte(renderBadge("uptime", value, color)))
	})

	promURL := os.Getenv("PROMETHEUS_URL")
	if promURL == "" {
		promURL = "http://grafana-kube-prometheus-st-prometheus.monitoring.svc:9090"
	}
	nodes := &nodesCache{prom: &promClient{base: promURL, http: &http.Client{Timeout: 5 * time.Second}}}

	router.GET("/api/nodes", func(c *gin.Context) {
		resp, err := nodes.get(c.Request.Context())
		if err != nil {
			log.Printf("could not collect node stats: %v", err)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "node stats not available"})
			return
		}
		c.JSON(http.StatusOK, resp)
	})

	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
			writeMetrics(w, dataFile)
		})
		log.Printf("metrics listening on :9100")
		if err := http.ListenAndServe(":9100", mux); err != nil {
			log.Fatalf("metrics server failed: %v", err)
		}
	}()

	addr := ":8080"
	log.Printf("status-api listening on %s", addr)
	if err := router.Run(addr); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
