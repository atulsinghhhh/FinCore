// Package httpapi exposes FinCore over a JSON REST API.
//
// Three audiences, three route groups:
//
//	/v1/...        merchants, authenticated with an API key (Bearer fc_test_...)
//	/v1/admin/...  platform operators, authenticated with the admin token
//	public         /healthz, /openapi.yaml, provider webhooks (HMAC-signed),
//	               /v1/payments/{id}/confirm (authorized by the provider's
//	               signature, so it can be called from the customer's browser)
package httpapi

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"fincore/internal/idempotency"
	"fincore/internal/ledger"
	"fincore/internal/metrics"
	"fincore/internal/operators"
	"fincore/internal/payments"
	"fincore/internal/provider/razorpay"
	"fincore/internal/ratelimit"
)

//go:embed openapi.yaml
var openAPISpec []byte

type HealthCheck func(ctx context.Context) error

type Server struct {
	// Mode is reported in the FinCore-Mode header and /healthz.
	Mode     string
	payments *payments.Service
	ledger   *ledger.Ledger
	idem     *idempotency.Store
	health   map[string]HealthCheck
	// AdminToken authenticates /v1/admin. Empty disables the admin API.
	AdminToken string
	// Sandbox enables endpoints that simulate the bank and the dev checkout
	// page. Never in production.
	Sandbox bool
	// Razorpay enables /webhooks/razorpay when set.
	Razorpay *razorpay.Client
	// Redis, when set, is a fast-path dedupe cache for webhook deliveries.
	Redis redis.UniversalClient
	// Limiter, when set, rate-limits /v1 per API client.
	Limiter *ratelimit.Limiter
	// Operators, when set, enables dashboard account management.
	Operators *operators.Service
}

func NewServer(svc *payments.Service, l *ledger.Ledger, idem *idempotency.Store, health map[string]HealthCheck) *Server {
	return &Server{payments: svc, ledger: l, idem: idem, health: health}
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, accessLog, metrics.Middleware, middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("FinCore-Mode", s.mode())
			next.ServeHTTP(w, r)
		})
	})

	r.Get("/healthz", s.healthz)
	r.Get("/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(openAPISpec)
	})
	r.Post("/webhooks/razorpay", s.razorpayWebhook)
	if s.Sandbox {
		r.Get("/dev/checkout/{id}", s.devCheckout)
	}

	r.Route("/v1", func(r chi.Router) {
		// Public: the provider's signature is the credential.
		r.Group(func(r chi.Router) {
			r.Use(s.limit(func(r *http.Request) string { return "ip:" + r.RemoteAddr }))
			r.Use(s.idem.Middleware(func(*http.Request) string { return "public" }))
			r.Post("/payments/{id}/confirm", s.confirmPayment)
		})

		// Merchant API.
		r.Group(func(r chi.Router) {
			r.Use(s.merchantAuth)
			r.Use(s.limit(func(r *http.Request) string { return "m:" + merchantFrom(r).String() }))
			r.Use(s.idem.Middleware(func(r *http.Request) string { return "m:" + merchantFrom(r).String() }))
			s.merchantRoutes(r)
		})

		// Admin API.
		r.Route("/admin", func(r chi.Router) {
			r.Use(s.adminAuth)
			// Settlement files can be large and are idempotent by content hash.
			r.With(bodyLimit(20<<20)).Post("/settlements", s.importSettlement)
			if s.Operators != nil {
				r.Group(s.operatorAuthRoutes)
			}
			r.Group(func(r chi.Router) {
				r.Use(s.idem.Middleware(func(*http.Request) string { return "admin" }))
				s.adminRoutes(r)
				if s.Operators != nil {
					s.operatorRoutes(r)
				}
			})
		})
	})
	return r
}

func (s *Server) mode() string {
	if s.Mode == "" {
		return "test"
	}
	return s.Mode
}

func (s *Server) limit(key func(*http.Request) string) func(http.Handler) http.Handler {
	if s.Limiter == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return s.Limiter.Middleware(key)
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	status, code := map[string]string{"mode": s.mode()}, http.StatusOK
	for name, check := range s.health {
		if err := check(ctx); err != nil {
			status[name], code = err.Error(), http.StatusServiceUnavailable
		} else {
			status[name] = "ok"
		}
	}
	writeJSON(w, code, status)
}

// ---- helpers ----------------------------------------------------------------

// decode reads a JSON body, rejecting unknown fields. An empty body is
// treated as {} so optional-body endpoints (capture, cancel) work without one.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", fmt.Sprintf("invalid request body: %v", err))
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	return pathUUID(w, r, "id")
}

func pathUUID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
		return uuid.Nil, false
	}
	return id, true
}

func bodyLimit(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}

func respond(w http.ResponseWriter, r *http.Request, status int, body any, err error) {
	if err != nil {
		code, errCode := classify(err)
		if code >= 500 {
			slog.ErrorContext(r.Context(), "request failed", "err", err, "request_id", middleware.GetReqID(r.Context()))
			writeError(w, code, errCode, http.StatusText(code))
			return
		}
		msg := strings.TrimPrefix(err.Error(), "ledger: invalid request: ")
		if errCode == "insufficient_funds" { // ledger detail names internal accounts
			msg = "the merchant's available balance is too low for this operation"
		}
		writeError(w, code, errCode, msg)
		return
	}
	writeJSON(w, status, body)
}

func classify(err error) (int, string) {
	switch {
	case errors.Is(err, payments.ErrNotFound), errors.Is(err, ledger.ErrAccountNotFound), errors.Is(err, ledger.ErrTransactionNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, payments.ErrUnauthenticated), errors.Is(err, payments.ErrWrongMode):
		return http.StatusUnauthorized, "unauthenticated"
	case errors.Is(err, payments.ErrIdempotencyKeyReused):
		return http.StatusUnprocessableEntity, "idempotency_key_reused"
	case errors.Is(err, payments.ErrInsufficientFunds):
		return http.StatusUnprocessableEntity, "insufficient_funds"
	case errors.Is(err, payments.ErrInvalidTransition):
		return http.StatusConflict, "invalid_state"
	case errors.Is(err, payments.ErrInvalidRequest):
		return http.StatusBadRequest, "invalid_request"
	case errors.Is(err, payments.ErrProviderUnavailable):
		return http.StatusBadGateway, "provider_unavailable"
	}
	return http.StatusInternalServerError, "internal_error"
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		slog.InfoContext(r.Context(), "http",
			"method", r.Method, "path", r.URL.Path, "status", ww.Status(),
			"duration_ms", time.Since(start).Milliseconds(), "request_id", middleware.GetReqID(r.Context()),
			"replayed", ww.Header().Get(idempotency.ReplayedHeader) == "true")
	})
}

// listFilter reads ?limit=&cursor=&status= query parameters.
func listFilter(r *http.Request) (payments.ListFilter, error) {
	f := payments.ListFilter{Status: r.URL.Query().Get("status"), Cursor: r.URL.Query().Get("cursor")}
	if v := r.URL.Query().Get("limit"); v != "" {
		var n int32
		if _, err := fmt.Sscan(v, &n); err != nil {
			return f, fmt.Errorf("%w: limit must be a number", payments.ErrInvalidRequest)
		}
		f.Limit = n
	}
	return f, nil
}

// period reads ?from=&to= (RFC 3339 or YYYY-MM-DD), defaulting to the last 30 days.
func period(r *http.Request) (time.Time, time.Time, error) {
	parse := func(name string, fallback time.Time) (time.Time, error) {
		v := r.URL.Query().Get(name)
		if v == "" {
			return fallback, nil
		}
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02"} {
			if t, err := time.Parse(layout, v); err == nil {
				return t, nil
			}
		}
		return time.Time{}, fmt.Errorf("%w: %s must be RFC 3339 or YYYY-MM-DD", payments.ErrInvalidRequest, name)
	}
	to, err := parse("to", time.Now().Add(time.Second))
	if err != nil {
		return to, to, err
	}
	from, err := parse("from", to.AddDate(0, 0, -30))
	return from, to, err
}
