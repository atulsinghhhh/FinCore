// Package idempotency makes POST requests safe to retry. The first request
// with a given Idempotency-Key runs; its response is stored in Dragonfly and
// replayed byte-for-byte to every retry for 24 hours.
//
// Dragonfly is a speed layer only. Every write endpoint also has a durable
// backstop in Postgres (unique idempotency keys on payments, refunds,
// payouts and ledger transactions, plus state-machine guards), so losing
// Dragonfly can never cause money to move twice.
package idempotency

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"

	"fincore/internal/metrics"
)

const (
	Header         = "Idempotency-Key"
	ReplayedHeader = "Idempotent-Replayed"
	maxKeyLength   = 255
	maxBodyBytes   = 1 << 20
)

type Store struct {
	rdb redis.UniversalClient
	// LockTTL bounds how long a crashed request blocks retries.
	LockTTL time.Duration
	// TTL is how long completed responses are replayed.
	TTL    time.Duration
	Prefix string
}

func NewStore(rdb redis.UniversalClient) *Store {
	return &Store{rdb: rdb, LockTTL: time.Minute, TTL: 24 * time.Hour, Prefix: "idem:"}
}

type record struct {
	State       string `json:"state"` // "in_progress" or "done"
	Token       string `json:"token,omitempty"`
	RequestHash string `json:"request_hash"`
	StatusCode  int    `json:"status_code,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Location    string `json:"location,omitempty"`
	Body        []byte `json:"body,omitempty"`
}

// Only overwrite / delete the key if it still holds our in-progress record,
// so a request whose lock expired cannot clobber whoever took over.
var (
	completeScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("SET", KEYS[1], ARGV[2], "EX", ARGV[3])
end
return false`)
	releaseScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0`)
)

// Middleware enforces Idempotency-Key on POST requests. scope partitions the
// key space (e.g. per merchant once auth exists); it may return "".
func (s *Store) Middleware(scope func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				next.ServeHTTP(w, r)
				return
			}
			key := r.Header.Get(Header)
			if key == "" || len(key) > maxKeyLength {
				writeError(w, http.StatusBadRequest, "idempotency_key_required",
					"POST requests need an Idempotency-Key header of 1-255 characters")
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
			if err != nil || len(body) > maxBodyBytes {
				writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body is too large or unreadable")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))

			s.serve(w, r, next, s.Prefix+scope(r)+":"+key, fingerprint(r, body))
		})
	}
}

func (s *Store) serve(w http.ResponseWriter, r *http.Request, next http.Handler, redisKey, hash string) {
	ctx := r.Context()
	lock := record{State: "in_progress", Token: newToken(), RequestHash: hash}
	lockJSON, _ := json.Marshal(lock)

	acquired, err := s.rdb.SetNX(ctx, redisKey, lockJSON, s.LockTTL).Result()
	if err != nil {
		// Fail open: the Postgres backstops still prevent duplicate effects.
		slog.WarnContext(ctx, "idempotency store unavailable, relying on database backstop", "err", err)
		next.ServeHTTP(w, r)
		return
	}

	if !acquired {
		raw, err := s.rdb.Get(ctx, redisKey).Bytes()
		if errors.Is(err, redis.Nil) { // expired between SETNX and GET
			s.serve(w, r, next, redisKey, hash)
			return
		}
		if err != nil {
			slog.WarnContext(ctx, "idempotency store unavailable, relying on database backstop", "err", err)
			next.ServeHTTP(w, r)
			return
		}
		var existing record
		if err := json.Unmarshal(raw, &existing); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "corrupt idempotency record")
			return
		}
		switch {
		case existing.RequestHash != hash:
			writeError(w, http.StatusUnprocessableEntity, "idempotency_key_reused",
				"this Idempotency-Key was already used with a different request")
		case existing.State == "in_progress":
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusConflict, "request_in_progress",
				"a request with this Idempotency-Key is still being processed")
		default:
			replay(w, existing)
		}
		return
	}

	rec := &recorder{ResponseWriter: w, status: http.StatusOK}
	completed := false
	defer func() {
		if completed {
			return
		}
		// Panic or server error: free the key so the client can retry.
		// Use a fresh context: the request's may already be canceled.
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if err := releaseScript.Run(cctx, s.rdb, []string{redisKey}, lockJSON).Err(); err != nil && !errors.Is(err, redis.Nil) {
			slog.WarnContext(ctx, "release idempotency lock", "err", err)
		}
	}()

	next.ServeHTTP(rec, r)

	// 5xx responses are not stored: the outcome is unknown, so a retry must
	// run again (and hits the database backstops if the first one landed).
	if rec.status >= 500 {
		return
	}
	done, _ := json.Marshal(record{
		State: "done", RequestHash: hash, StatusCode: rec.status,
		ContentType: rec.Header().Get("Content-Type"), Location: rec.Header().Get("Location"),
		Body: rec.body.Bytes(),
	})
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := completeScript.Run(cctx, s.rdb, []string{redisKey}, lockJSON, done, int(s.TTL.Seconds())).Err(); err != nil && !errors.Is(err, redis.Nil) {
		slog.WarnContext(ctx, "store idempotent response", "err", err)
		return
	}
	completed = true
}

func replay(w http.ResponseWriter, rec record) {
	if rec.ContentType != "" {
		w.Header().Set("Content-Type", rec.ContentType)
	}
	if rec.Location != "" {
		w.Header().Set("Location", rec.Location)
	}
	metrics.IdempotentReplays.Inc()
	w.Header().Set(ReplayedHeader, "true")
	w.WriteHeader(rec.StatusCode)
	_, _ = w.Write(rec.Body)
}

// fingerprint binds the key to the exact endpoint and body, so reusing a key
// for a different request is detected instead of silently replayed.
func fingerprint(r *http.Request, body []byte) string {
	h := sha256.New()
	h.Write([]byte(r.Method + " " + r.URL.Path + "\n"))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

func newToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type recorder struct {
	http.ResponseWriter
	status      int
	body        bytes.Buffer
	wroteHeader bool
}

func (r *recorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status, r.wroteHeader = code, true
		r.ResponseWriter.WriteHeader(code)
	}
}

func (r *recorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
