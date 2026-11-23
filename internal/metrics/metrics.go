// Package metrics defines FinCore's Prometheus metrics.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	Registry = prometheus.NewRegistry()

	HTTPRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "fincore_http_requests_total", Help: "HTTP requests by route and status.",
	}, []string{"method", "route", "status"})
	HTTPDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "fincore_http_request_duration_seconds", Help: "HTTP request latency.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
	}, []string{"method", "route"})
	IdempotentReplays = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "fincore_idempotent_replays_total", Help: "Responses served from the idempotency store.",
	})
	RateLimited = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "fincore_rate_limited_total", Help: "Requests rejected by the rate limiter.",
	})
	PaymentTransitions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "fincore_payment_transitions_total", Help: "Payment state transitions by provider and new status.",
	}, []string{"provider", "status"})
	ProviderWebhooks = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "fincore_provider_webhooks_total", Help: "Inbound provider webhooks by result.",
	}, []string{"provider", "result"})
	OutboxDeliveries = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "fincore_outbox_deliveries_total", Help: "Outbox delivery attempts by result.",
	}, []string{"result"})
	OutboxEvents = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fincore_outbox_events", Help: "Outbox events by status.",
	}, []string{"status"})
	OutboxOldestPending = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "fincore_outbox_oldest_pending_seconds", Help: "Age of the oldest undelivered outbox event.",
	})
	SweeperPayments = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "fincore_sweeper_payments_total", Help: "Stale payments recovered or expired by the sweeper.",
	}, []string{"action"})
	LedgerIntegrityOK = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "fincore_ledger_integrity_ok", Help: "1 if the last ledger integrity check passed, 0 if it failed.",
	})
	LedgerIntegrityChecked = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "fincore_ledger_integrity_last_check_timestamp_seconds", Help: "When the ledger was last checked.",
	})
)

func init() {
	Registry.MustRegister(
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		HTTPRequests, HTTPDuration, IdempotentReplays, RateLimited, PaymentTransitions, ProviderWebhooks,
		OutboxDeliveries, OutboxEvents, OutboxOldestPending, SweeperPayments, LedgerIntegrityOK, LedgerIntegrityChecked,
	)
}

func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{})
}

// Middleware records request count and latency labelled by chi route
// pattern (e.g. /v1/payments/{id}), never by raw path, to keep cardinality low.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		route := "unmatched"
		if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
			route = rc.RoutePattern()
		}
		HTTPRequests.WithLabelValues(r.Method, route, strconv.Itoa(ww.Status())).Inc()
		HTTPDuration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
	})
}
