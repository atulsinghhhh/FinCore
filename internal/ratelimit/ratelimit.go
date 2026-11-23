// Package ratelimit is a fixed-window rate limiter in Dragonfly.
package ratelimit

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"fincore/internal/metrics"
)

// INCR and set the window's expiry atomically; returns {count, ttl_ms}.
var script = redis.NewScript(`
local n = redis.call("INCR", KEYS[1])
if n == 1 then redis.call("PEXPIRE", KEYS[1], ARGV[1]) end
return {n, redis.call("PTTL", KEYS[1])}`)

type Limiter struct {
	rdb    redis.UniversalClient
	Limit  int
	Window time.Duration
	Prefix string
}

func New(rdb redis.UniversalClient, limit int, window time.Duration) *Limiter {
	return &Limiter{rdb: rdb, Limit: limit, Window: window, Prefix: "rl:"}
}

// Middleware limits requests per key. It fails open: if Dragonfly is
// unavailable, requests are allowed and a warning is logged.
func (l *Limiter) Middleware(key func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			res, err := script.Run(r.Context(), l.rdb, []string{l.Prefix + key(r)}, l.Window.Milliseconds()).Int64Slice()
			if err != nil || len(res) != 2 {
				slog.WarnContext(r.Context(), "rate limiter unavailable, allowing request", "err", err)
				next.ServeHTTP(w, r)
				return
			}
			count, ttl := int(res[0]), time.Duration(res[1])*time.Millisecond
			remaining := max(l.Limit-count, 0)
			h := w.Header()
			h.Set("X-RateLimit-Limit", strconv.Itoa(l.Limit))
			h.Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			h.Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(ttl).Unix(), 10))
			if count > l.Limit {
				metrics.RateLimited.Inc()
				h.Set("Retry-After", strconv.Itoa(int(ttl.Seconds())+1))
				h.Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":{"code":"rate_limited","message":"too many requests"}}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
