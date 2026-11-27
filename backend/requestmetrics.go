package main

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/trace"
)

// How long each request took, per route, with the trace id of a recent
// request in each bucket as an exemplar. In Grafana a dot on the latency
// chart opens that request's trace in Tempo: from "this got slow" to
// the span that was slow, in one click.
//
// Its own registry and path ( /metrics/requests on :9100 ), served as
// OpenMetrics, the format exemplars need; /metrics stays the hand-written
// text of the checker's results ( metrics.go ).

var (
	requestRegistry = prometheus.NewRegistry()
	requestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "status_api_request_duration_seconds",
		Help: "Time to answer a request, by route and status code.",
		// Most answers come from a cache in under 5 ms; a cache miss asks
		// Prometheus or GitHub and takes 50 ms to a few seconds.
		Buckets: []float64{0.005, 0.025, 0.1, 0.25, 0.5, 1, 2.5, 5},
	}, []string{"route", "code"})
)

func init() {
	requestRegistry.MustRegister(requestDuration)
}

func requestMetrics() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		if !notHealthz(c.Request) {
			return
		}
		route := c.FullPath() // the pattern, not the path: /badge/:file
		if route == "" {
			route = "unmatched"
		}
		obs := requestDuration.WithLabelValues(route, strconv.Itoa(c.Writer.Status()))
		took := time.Since(start).Seconds()
		if sc := trace.SpanContextFromContext(c.Request.Context()); sc.IsSampled() {
			obs.(prometheus.ExemplarObserver).ObserveWithExemplar(took, prometheus.Labels{"trace_id": sc.TraceID().String()})
			return
		}
		obs.Observe(took)
	}
}

func requestMetricsHandler() http.Handler {
	return promhttp.HandlerFor(requestRegistry, promhttp.HandlerOpts{EnableOpenMetrics: true})
}
