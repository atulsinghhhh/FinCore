package httpapi

import (
	"encoding/json"
	"html/template"
	"net/http"

	"fincore/internal/payments"
)

// devCheckout serves a minimal page that opens Razorpay Checkout for a
// created payment and forwards the signed result to /confirm. It exists so
// the full flow can be tried locally with Razorpay test cards; a real
// merchant integrates Checkout in their own frontend.
func (s *Server) devCheckout(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p, err := s.payments.GetPayment(r.Context(), id)
	if err != nil {
		respond(w, r, 0, nil, err)
		return
	}
	var next struct {
		KeyID   string `json:"key_id"`
		OrderID string `json:"order_id"`
	}
	if p.Status != payments.StatusCreated || json.Unmarshal(p.NextAction, &next) != nil || next.OrderID == "" {
		writeError(w, http.StatusConflict, "invalid_state", "payment is not awaiting Razorpay checkout (status "+string(p.Status)+")")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = checkoutPage.Execute(w, map[string]any{
		"PaymentID": p.ID.String(), "KeyID": next.KeyID, "OrderID": next.OrderID,
		"Amount": p.Amount, "Rupees": float64(p.Amount) / 100, "Currency": p.Currency, "Description": p.Description,
	})
}

var checkoutPage = template.Must(template.New("checkout").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>FinCore test checkout</title>
<style>
 body{font:16px/1.5 system-ui,sans-serif;max-width:32rem;margin:3rem auto;padding:0 1rem;color:#1a1a1a}
 button{font:inherit;padding:.6rem 1.2rem;border:0;border-radius:6px;background:#2563eb;color:#fff;cursor:pointer}
 pre{background:#f4f4f5;padding:1rem;border-radius:6px;overflow:auto;font-size:13px}
 .muted{color:#666;font-size:14px}
</style></head><body>
<h1>Pay ₹{{printf "%.2f" .Rupees}}</h1>
<p class="muted">FinCore payment <code>{{.PaymentID}}</code><br>Razorpay order <code>{{.OrderID}}</code> (test mode)</p>
<button id="pay">Pay with Razorpay</button>
<p class="muted">Test card: 4111 1111 1111 1111, any future expiry, any CVV. Or UPI: success@razorpay.</p>
<pre id="out" hidden></pre>
<script src="https://checkout.razorpay.com/v1/checkout.js"></script>
<script>
const out = document.getElementById("out");
const show = (x) => { out.hidden = false; out.textContent = JSON.stringify(x, null, 2); };
document.getElementById("pay").onclick = () => {
  const rzp = new Razorpay({
    key: {{.KeyID}}, order_id: {{.OrderID}}, amount: {{.Amount}}, currency: {{.Currency}},
    name: "FinCore", description: {{.Description}},
    handler: async (resp) => {
      const r = await fetch("/v1/payments/{{.PaymentID}}/confirm", {
        method: "POST",
        headers: {"Content-Type": "application/json", "Idempotency-Key": "confirm-" + resp.razorpay_payment_id},
        body: JSON.stringify(resp),
      });
      show(await r.json());
    },
  });
  rzp.on("payment.failed", (resp) => show(resp.error));
  rzp.open();
};
</script></body></html>`))
