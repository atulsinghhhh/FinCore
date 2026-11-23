package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"fincore/internal/payments"
)

func (s *Server) adminRoutes(r chi.Router) {
	r.Get("/stats", s.adminStats)
	r.Get("/analytics", s.adminAnalytics)
	r.Get("/search", s.adminSearch)

	r.Post("/merchants", s.adminCreateMerchant)
	r.Get("/merchants", s.adminListMerchants)
	r.Get("/merchants/{id}", s.adminGetMerchant)
	r.Get("/merchants/{id}/balance", s.adminMerchantBalance)
	r.Get("/merchants/{id}/statement", s.adminMerchantStatement)
	r.Get("/merchants/{id}/api-keys", s.adminListAPIKeys)
	r.Post("/merchants/{id}/api-keys", s.adminCreateAPIKey)
	r.Post("/merchants/{id}/api-keys/{keyID}/revoke", s.adminRevokeAPIKey)

	r.Get("/payments", s.adminListPayments)
	r.Get("/payments/{id}", s.adminGetPayment)
	r.Get("/payouts", s.adminListPayouts)
	if s.Sandbox {
		r.Post("/sandbox/payouts/{id}/paid", s.sandboxPayoutPaid)
		r.Post("/sandbox/payouts/{id}/failed", s.sandboxPayoutFailed)
	}

	r.Get("/ledger/accounts", s.adminAccounts)
	r.Get("/ledger/integrity", s.adminIntegrity)
	r.Get("/ledger/transactions/{id}", s.adminLedgerTransaction)

	r.Get("/settlements", s.adminListSettlements)
	r.Get("/settlements/{id}", s.adminGetSettlement)

	r.Get("/outbox", s.adminListOutbox)
	r.Post("/outbox/{eventID}/requeue", s.adminRequeueOutbox)
}

func (s *Server) adminAnalytics(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	a, err := s.payments.Analytics(r.Context(), days)
	respond(w, r, http.StatusOK, a, err)
}

func (s *Server) adminSearch(w http.ResponseWriter, r *http.Request) {
	res, err := s.payments.Search(r.Context(), r.URL.Query().Get("q"))
	respond(w, r, http.StatusOK, res, err)
}

func (s *Server) adminStats(w http.ResponseWriter, r *http.Request) {
	st, err := s.payments.Stats(r.Context())
	respond(w, r, http.StatusOK, st, err)
}

// adminCreateMerchant returns the merchant and its first API key. The key's
// secret is shown only in this response.
func (s *Server) adminCreateMerchant(w http.ResponseWriter, r *http.Request) {
	req := payments.CreateMerchantRequest{FeeBps: 200}
	if !decode(w, r, &req) {
		return
	}
	m, key, err := s.payments.CreateMerchantWithKey(r.Context(), req)
	respond(w, r, http.StatusCreated, map[string]any{"merchant": m, "api_key": key}, err)
}

func (s *Server) adminListMerchants(w http.ResponseWriter, r *http.Request) {
	list, err := s.payments.ListMerchants(r.Context(), 200)
	respond(w, r, http.StatusOK, map[string]any{"data": list}, err)
}

func (s *Server) adminGetMerchant(w http.ResponseWriter, r *http.Request) {
	if id, ok := pathID(w, r); ok {
		m, err := s.payments.GetMerchant(r.Context(), id)
		respond(w, r, http.StatusOK, m, err)
	}
}

func (s *Server) adminMerchantBalance(w http.ResponseWriter, r *http.Request) {
	if id, ok := pathID(w, r); ok {
		b, err := s.payments.MerchantBalance(r.Context(), id)
		respond(w, r, http.StatusOK, b, err)
	}
}

func (s *Server) adminMerchantStatement(w http.ResponseWriter, r *http.Request) {
	if id, ok := pathID(w, r); ok {
		s.statement(w, r, id)
	}
}

func (s *Server) statement(w http.ResponseWriter, r *http.Request, merchantID uuid.UUID) {
	from, to, err := period(r)
	if err != nil {
		respond(w, r, 0, nil, err)
		return
	}
	m, err := s.payments.GetMerchant(r.Context(), merchantID)
	if err != nil {
		respond(w, r, 0, nil, err)
		return
	}
	st, err := s.ledger.Statement(r.Context(), m.PayableAccountID, from, to)
	respond(w, r, http.StatusOK, st, err)
}

func (s *Server) adminListAPIKeys(w http.ResponseWriter, r *http.Request) {
	if id, ok := pathID(w, r); ok {
		keys, err := s.payments.ListAPIKeys(r.Context(), id)
		respond(w, r, http.StatusOK, map[string]any{"data": keys}, err)
	}
}

func (s *Server) adminCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	if id, ok := pathID(w, r); ok {
		k, err := s.payments.CreateAPIKey(r.Context(), id)
		if err == nil {
			s.auditMerchant(r, id, "api_key.issued", k.Prefix+"…")
		}
		respond(w, r, http.StatusCreated, k, err)
	}
}

// auditMerchant records a merchant-level change in the operator audit trail.
// Best effort: the change has already happened.
func (s *Server) auditMerchant(r *http.Request, merchantID uuid.UUID, action, detail string) {
	if s.Operators == nil {
		return
	}
	target := merchantID.String()
	if m, err := s.payments.GetMerchant(r.Context(), merchantID); err == nil {
		target = m.Name
	}
	if err := s.Operators.Record(r.Context(), actor(r), action, target, detail); err != nil {
		slog.WarnContext(r.Context(), "audit merchant change", "err", err)
	}
}

func (s *Server) adminRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	keyID, ok := pathUUID(w, r, "keyID")
	if !ok {
		return
	}
	k, err := s.payments.RevokeAPIKey(r.Context(), id, keyID)
	if err == nil {
		s.auditMerchant(r, id, "api_key.revoked", k.Prefix+"…")
	}
	respond(w, r, http.StatusOK, k, err)
}

func (s *Server) adminListPayments(w http.ResponseWriter, r *http.Request) {
	f, err := listFilter(r)
	if err == nil && r.URL.Query().Get("merchant_id") != "" {
		id, perr := uuid.Parse(r.URL.Query().Get("merchant_id"))
		if perr != nil {
			err = payments.ErrInvalidRequest
		}
		f.MerchantID = &id
	}
	if err != nil {
		respond(w, r, 0, nil, err)
		return
	}
	list, err := s.payments.ListPayments(r.Context(), f)
	respond(w, r, http.StatusOK, list, err)
}

func (s *Server) adminGetPayment(w http.ResponseWriter, r *http.Request) {
	if id, ok := pathID(w, r); ok {
		d, err := s.payments.GetPaymentDetail(r.Context(), id)
		respond(w, r, http.StatusOK, d, err)
	}
}

func (s *Server) adminListPayouts(w http.ResponseWriter, r *http.Request) {
	f, err := listFilter(r)
	if err == nil && r.URL.Query().Get("merchant_id") != "" {
		id, perr := uuid.Parse(r.URL.Query().Get("merchant_id"))
		if perr != nil {
			err = payments.ErrInvalidRequest
		}
		f.MerchantID = &id
	}
	if err != nil {
		respond(w, r, 0, nil, err)
		return
	}
	list, err := s.payments.ListPayouts(r.Context(), f)
	respond(w, r, http.StatusOK, list, err)
}

func (s *Server) sandboxPayoutPaid(w http.ResponseWriter, r *http.Request) {
	if id, ok := pathID(w, r); ok {
		p, err := s.payments.CompletePayout(r.Context(), id)
		respond(w, r, http.StatusOK, p, err)
	}
}

func (s *Server) sandboxPayoutFailed(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if !decode(w, r, &req) {
		return
	}
	p, err := s.payments.FailPayout(r.Context(), id, req.Reason)
	respond(w, r, http.StatusOK, p, err)
}

func (s *Server) adminAccounts(w http.ResponseWriter, r *http.Request) {
	list, err := s.ledger.AccountBalances(r.Context(), r.URL.Query().Get("prefix"))
	respond(w, r, http.StatusOK, map[string]any{"data": list}, err)
}

func (s *Server) adminIntegrity(w http.ResponseWriter, r *http.Request) {
	report, err := s.ledger.CheckIntegrity(r.Context())
	respond(w, r, http.StatusOK, map[string]any{
		"ok": report.OK(), "trial_balance": report.TrialBalance, "mismatches": report.Mismatches,
	}, err)
}

func (s *Server) adminLedgerTransaction(w http.ResponseWriter, r *http.Request) {
	if id, ok := pathID(w, r); ok {
		t, err := s.ledger.GetTransaction(r.Context(), id)
		respond(w, r, http.StatusOK, t, err)
	}
}

// importSettlement takes a settlement CSV as the raw request body:
//
//	POST /v1/admin/settlements?provider=razorpay&source=setl_2026-11-24.csv
func (s *Server) importSettlement(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "settlement file is too large (max 20 MB)")
		return
	}
	providerName := r.URL.Query().Get("provider")
	source := r.URL.Query().Get("source")
	if source == "" {
		source = "upload"
	}
	report, err := s.payments.Reconcile(r.Context(), providerName, source, data)
	status := http.StatusCreated
	if report.Replayed {
		status = http.StatusOK
	}
	respond(w, r, status, report, err)
}

func (s *Server) adminListSettlements(w http.ResponseWriter, r *http.Request) {
	list, err := s.payments.ListSettlementReports(r.Context(), 100)
	respond(w, r, http.StatusOK, map[string]any{"data": list}, err)
}

func (s *Server) adminGetSettlement(w http.ResponseWriter, r *http.Request) {
	if id, ok := pathID(w, r); ok {
		report, err := s.payments.GetSettlementReport(r.Context(), id)
		respond(w, r, http.StatusOK, report, err)
	}
}

func (s *Server) adminListOutbox(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var merchantID *uuid.UUID
	if v := q.Get("merchant_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			respond(w, r, 0, nil, payments.ErrInvalidRequest)
			return
		}
		merchantID = &id
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, err := s.payments.ListOutbox(r.Context(), q.Get("status"), merchantID, q.Get("cursor"), int32(limit))
	respond(w, r, http.StatusOK, list, err)
}

func (s *Server) adminRequeueOutbox(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "eventID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	err = s.payments.RequeueOutbox(r.Context(), id)
	respond(w, r, http.StatusOK, map[string]any{"id": id, "status": "pending"}, err)
}
