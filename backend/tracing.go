package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Traces go to Alloy (observability/) over OTLP, only when
// OTEL_EXPORTER_OTLP_ENDPOINT is set (the Deployment sets it); the SDK
// reads that and OTEL_SERVICE_NAME itself. Without it, nothing is
// exported and the spans cost next to nothing.
func initTracing() func(context.Context) error {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return func(context.Context) error { return nil }
	}
	exp, err := otlptracehttp.New(context.Background())
	if err != nil {
		log.Printf("tracing off: %v", err)
		return func(context.Context) error { return nil }
	}
	res, _ := sdkresource.Merge(sdkresource.Default(), sdkresource.Environment())
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	log.Printf("tracing to %s", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	return tp.Shutdown
}

// tracedTransport makes outgoing requests (Prometheus, GitHub) child spans
// of the request that caused them.
func tracedTransport() http.RoundTripper {
	return otelhttp.NewTransport(http.DefaultTransport)
}

// The kubelet's probes would be most of the traces and log lines.
func notHealthz(r *http.Request) bool { return r.URL.Path != "/healthz" }

// tracing starts a span per request (not /healthz).
func tracing() gin.HandlerFunc {
	return otelgin.Middleware("status-api", otelgin.WithFilter(notHealthz))
}

// requestLog replaces gin's own logger: the same facts, plus the trace id,
// which Alloy picks up so Grafana can link a log line to its trace.
func requestLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		if !notHealthz(c.Request) {
			return
		}
		line := []any{c.Writer.Status(), time.Since(start).Round(time.Microsecond), c.Request.Method, c.Request.URL.Path}
		if sc := trace.SpanContextFromContext(c.Request.Context()); sc.HasTraceID() {
			log.Printf("%d %v %s %s trace_id=%s", append(line, sc.TraceID())...)
			return
		}
		log.Printf("%d %v %s %s", line...)
	}
}
