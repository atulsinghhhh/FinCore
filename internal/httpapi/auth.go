package httpapi

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"fincore/internal/payments"
)

type ctxKey struct{}

func merchantFrom(r *http.Request) uuid.UUID {
	id, _ := r.Context().Value(ctxKey{}).(uuid.UUID)
	return id
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// merchantAuth resolves "Authorization: Bearer fc_test_..." to a merchant.
func (s *Server) merchantAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		merchantID, err := s.payments.Authenticate(r.Context(), bearer(r))
		if errors.Is(err, payments.ErrWrongMode) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="fincore"`)
			writeError(w, http.StatusUnauthorized, "wrong_mode", strings.TrimPrefix(err.Error(), payments.ErrWrongMode.Error()+": "))
			return
		}
		if errors.Is(err, payments.ErrUnauthenticated) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="fincore"`)
			writeError(w, http.StatusUnauthorized, "unauthenticated", "missing, invalid or revoked API key")
			return
		}
		if err != nil {
			respond(w, r, 0, nil, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, merchantID)))
	})
}

// adminAuth checks the admin token in constant time.
func (s *Server) adminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearer(r)
		if s.AdminToken == "" || subtle.ConstantTimeCompare([]byte(token), []byte(s.AdminToken)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="fincore-admin"`)
			writeError(w, http.StatusUnauthorized, "unauthenticated", "admin token required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ownPayment loads a payment and hides it unless it belongs to the caller.
func (s *Server) ownPayment(w http.ResponseWriter, r *http.Request) (payments.Payment, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return payments.Payment{}, false
	}
	p, err := s.payments.GetPayment(r.Context(), id)
	if err == nil && p.MerchantID != merchantFrom(r) {
		err = payments.ErrNotFound // never reveal another merchant's resources
	}
	if err != nil {
		respond(w, r, 0, nil, err)
		return payments.Payment{}, false
	}
	return p, true
}
