package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"fincore/internal/idempotency"
	"fincore/internal/payments"
)

func (s *Server) merchantRoutes(r chi.Router) {
	r.Get("/merchants/me", s.getMe)
	r.Get("/merchants/me/balance", s.getMyBalance)
	r.Get("/merchants/me/statement", s.getMyStatement)
	r.Post("/merchants/me/webhook-endpoint", s.setWebhookEndpoint)

	r.Post("/payments", s.createPayment)
	r.Get("/payments", s.listPayments)
	r.Get("/payments/{id}", s.getPayment)
	r.Get("/payments/{id}/events", s.listPaymentEvents)
	r.Post("/payments/{id}/capture", s.capturePayment)
	r.Post("/payments/{id}/cancel", s.cancelPayment)
	r.Post("/payments/{id}/refunds", s.createRefund)
	r.Get("/payments/{id}/refunds", s.listRefunds)

	r.Post("/payouts", s.createPayout)
	r.Get("/payouts", s.listPayouts)
	r.Get("/payouts/{id}", s.getPayout)
}

func (s *Server) getMe(w http.ResponseWriter, r *http.Request) {
	m, err := s.payments.GetMerchant(r.Context(), merchantFrom(r))
	respond(w, r, http.StatusOK, m, err)
}

func (s *Server) getMyBalance(w http.ResponseWriter, r *http.Request) {
	b, err := s.payments.MerchantBalance(r.Context(), merchantFrom(r))
	respond(w, r, http.StatusOK, b, err)
}

func (s *Server) getMyStatement(w http.ResponseWriter, r *http.Request) {
	s.statement(w, r, merchantFrom(r))
}

func (s *Server) setWebhookEndpoint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if !decode(w, r, &req) {
		return
	}
	ep, err := s.payments.SetMerchantWebhook(r.Context(), merchantFrom(r), req.URL)
	respond(w, r, http.StatusOK, ep, err)
}

func (s *Server) createPayment(w http.ResponseWriter, r *http.Request) {
	var req payments.CreatePaymentRequest
	if !decode(w, r, &req) {
		return
	}
	req.MerchantID = merchantFrom(r)
	req.IdempotencyKey = r.Header.Get(idempotency.Header)
	p, err := s.payments.CreatePayment(r.Context(), req)
	if err == nil {
		w.Header().Set("Location", "/v1/payments/"+p.ID.String())
	}
	respond(w, r, http.StatusCreated, p, err)
}

func (s *Server) listPayments(w http.ResponseWriter, r *http.Request) {
	f, err := listFilter(r)
	if err == nil {
		me := merchantFrom(r)
		f.MerchantID = &me
		list, err := s.payments.ListPayments(r.Context(), f)
		respond(w, r, http.StatusOK, list, err)
		return
	}
	respond(w, r, 0, nil, err)
}

func (s *Server) getPayment(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.ownPayment(w, r); ok {
		writeJSON(w, http.StatusOK, p)
	}
}

func (s *Server) listPaymentEvents(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.ownPayment(w, r); ok {
		events, err := s.payments.ListPaymentEvents(r.Context(), p.ID)
		respond(w, r, http.StatusOK, map[string]any{"data": events}, err)
	}
}

// confirmPayment accepts the signed result from the provider's checkout
// (for Razorpay: razorpay_payment_id, razorpay_order_id, razorpay_signature).
// It needs no API key: only a provider-signed proof can move the payment,
// and only to the state the provider itself reports.
func (s *Server) confirmPayment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	params := map[string]string{}
	if !decode(w, r, &params) {
		return
	}
	p, err := s.payments.ConfirmPayment(r.Context(), id, params)
	respond(w, r, http.StatusOK, p, err)
}

func (s *Server) capturePayment(w http.ResponseWriter, r *http.Request) {
	p, ok := s.ownPayment(w, r)
	if !ok {
		return
	}
	var req payments.CaptureRequest
	if !decode(w, r, &req) {
		return
	}
	p, err := s.payments.Capture(r.Context(), p.ID, req)
	respond(w, r, http.StatusOK, p, err)
}

func (s *Server) cancelPayment(w http.ResponseWriter, r *http.Request) {
	p, ok := s.ownPayment(w, r)
	if !ok {
		return
	}
	p, err := s.payments.Cancel(r.Context(), p.ID)
	respond(w, r, http.StatusOK, p, err)
}

func (s *Server) createRefund(w http.ResponseWriter, r *http.Request) {
	p, ok := s.ownPayment(w, r)
	if !ok {
		return
	}
	var req payments.RefundRequest
	if !decode(w, r, &req) {
		return
	}
	req.IdempotencyKey = r.Header.Get(idempotency.Header)
	refund, err := s.payments.Refund(r.Context(), p.ID, req)
	respond(w, r, http.StatusCreated, refund, err)
}

func (s *Server) listRefunds(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.ownPayment(w, r); ok {
		refunds, err := s.payments.ListRefunds(r.Context(), p.ID)
		respond(w, r, http.StatusOK, map[string]any{"data": refunds}, err)
	}
}

func (s *Server) createPayout(w http.ResponseWriter, r *http.Request) {
	var req payments.CreatePayoutRequest
	if !decode(w, r, &req) {
		return
	}
	req.MerchantID = merchantFrom(r)
	req.IdempotencyKey = r.Header.Get(idempotency.Header)
	p, err := s.payments.CreatePayout(r.Context(), req)
	if err == nil {
		w.Header().Set("Location", "/v1/payouts/"+p.ID.String())
	}
	respond(w, r, http.StatusCreated, p, err)
}

func (s *Server) listPayouts(w http.ResponseWriter, r *http.Request) {
	f, err := listFilter(r)
	if err != nil {
		respond(w, r, 0, nil, err)
		return
	}
	me := merchantFrom(r)
	f.MerchantID = &me
	list, err := s.payments.ListPayouts(r.Context(), f)
	respond(w, r, http.StatusOK, list, err)
}

func (s *Server) getPayout(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p, err := s.payments.GetPayout(r.Context(), id)
	if err == nil && p.MerchantID != merchantFrom(r) {
		err = payments.ErrNotFound
	}
	respond(w, r, http.StatusOK, p, err)
}
