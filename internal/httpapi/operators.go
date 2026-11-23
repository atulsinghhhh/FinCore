package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"fincore/internal/operators"
)

// Operator management for the dashboard (admin token only). The dashboard
// enforces roles for the signed-in operator and passes their ID in
// FinCore-Operator-Id so every change is attributed in the audit trail.

func actor(r *http.Request) *uuid.UUID {
	id, err := uuid.Parse(r.Header.Get("FinCore-Operator-Id"))
	if err != nil {
		return nil
	}
	return &id
}

// operatorAuthRoutes carry passwords and invite tokens, so they bypass the
// idempotency cache (responses would otherwise be stored in Dragonfly).
func (s *Server) operatorAuthRoutes(r chi.Router) {
	r.Post("/operators/login", s.operatorLogin)
	r.Post("/invites", s.createInvite)
	r.Post("/invites/lookup", s.lookupInvite)
	r.Post("/invites/accept", s.acceptInvite)
}

func (s *Server) operatorRoutes(r chi.Router) {
	r.Get("/operators", s.listOperators)
	r.Get("/operators/events", s.operatorEvents)
	r.Get("/operators/{id}", s.getOperator)
	r.Post("/operators/{id}/access", s.setOperatorAccess)
	r.Post("/invites/{id}/revoke", s.revokeInvite)
}

func (s *Server) operatorLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	op, err := s.Operators.Login(r.Context(), req.Email, req.Password)
	respondOperators(w, r, http.StatusOK, op, err)
}

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if !decode(w, r, &req) {
		return
	}
	inv, token, err := s.Operators.Invite(r.Context(), actor(r), req.Email, req.Role)
	respondOperators(w, r, http.StatusCreated, map[string]any{"invite": inv, "token": token}, err)
}

func (s *Server) lookupInvite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &req) {
		return
	}
	inv, err := s.Operators.LookupInvite(r.Context(), req.Token)
	respondOperators(w, r, http.StatusOK, inv, err)
}

func (s *Server) acceptInvite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token    string `json:"token"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	op, err := s.Operators.AcceptInvite(r.Context(), req.Token, req.Name, req.Password)
	respondOperators(w, r, http.StatusCreated, op, err)
}

func (s *Server) listOperators(w http.ResponseWriter, r *http.Request) {
	ops, invites, err := s.Operators.List(r.Context())
	respondOperators(w, r, http.StatusOK, map[string]any{"operators": ops, "invites": invites}, err)
}

func (s *Server) operatorEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.Operators.Events(r.Context(), 50)
	respondOperators(w, r, http.StatusOK, map[string]any{"data": events}, err)
}

func (s *Server) getOperator(w http.ResponseWriter, r *http.Request) {
	if id, ok := pathID(w, r); ok {
		op, err := s.Operators.Get(r.Context(), id)
		respondOperators(w, r, http.StatusOK, op, err)
	}
}

func (s *Server) setOperatorAccess(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Role   string `json:"role"`
		Status string `json:"status"`
	}
	if !decode(w, r, &req) {
		return
	}
	op, err := s.Operators.SetAccess(r.Context(), actor(r), id, req.Role, req.Status)
	respondOperators(w, r, http.StatusOK, op, err)
}

func (s *Server) revokeInvite(w http.ResponseWriter, r *http.Request) {
	if id, ok := pathID(w, r); ok {
		err := s.Operators.RevokeInvite(r.Context(), actor(r), id)
		respondOperators(w, r, http.StatusOK, map[string]string{"status": "revoked"}, err)
	}
}

func respondOperators(w http.ResponseWriter, r *http.Request, status int, body any, err error) {
	switch {
	case err == nil:
		writeJSON(w, status, body)
	case errors.Is(err, operators.ErrBadCredentials):
		writeError(w, http.StatusUnauthorized, "bad_credentials", "incorrect email or password")
	case errors.Is(err, operators.ErrInviteInvalid):
		writeError(w, http.StatusGone, "invite_invalid", "this invite link is invalid, already used, revoked or expired")
	case errors.Is(err, operators.ErrLastAdmin):
		writeError(w, http.StatusConflict, "last_admin", "at least one active admin must remain")
	case errors.Is(err, operators.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", trimPrefix(err, operators.ErrConflict))
	case errors.Is(err, operators.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_request", trimPrefix(err, operators.ErrInvalid))
	case errors.Is(err, operators.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
	default:
		respond(w, r, 0, nil, err)
	}
}

func trimPrefix(err, sentinel error) string {
	msg := err.Error()
	if p := sentinel.Error() + ": "; len(msg) > len(p) && msg[:len(p)] == p {
		return msg[len(p):]
	}
	return msg
}
