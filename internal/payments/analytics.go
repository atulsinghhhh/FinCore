package payments

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"fincore/internal/store"
)

// IST is the business day boundary for analytics.
var IST = time.FixedZone("IST", 5*3600+1800)

type DayPoint struct {
	Day            string `json:"day"` // YYYY-MM-DD, IST
	CapturedVolume int64  `json:"captured_volume"`
	Succeeded      int64  `json:"succeeded"`
	Failed         int64  `json:"failed"`
	Total          int64  `json:"total"`
	RefundedVolume int64  `json:"refunded_volume"`
	Fees           int64  `json:"fees"`
}

type PeriodTotals struct {
	CapturedVolume int64 `json:"captured_volume"`
	Succeeded      int64 `json:"succeeded"`
	Failed         int64 `json:"failed"`
	Total          int64 `json:"total"`
	RefundedVolume int64 `json:"refunded_volume"`
	Fees           int64 `json:"fees"`
	// SuccessRate is succeeded / (succeeded + failed); payments still
	// awaiting the customer are not counted either way.
	SuccessRate float64 `json:"success_rate"`
	AvgTicket   int64   `json:"avg_ticket"`
}

type StatusCount struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
	Amount int64  `json:"amount"`
}

type MerchantVolume struct {
	ID             uuid.UUID `json:"id"`
	Name           string    `json:"name"`
	CapturedVolume int64     `json:"captured_volume"`
	Succeeded      int64     `json:"succeeded"`
}

type Analytics struct {
	Days         int              `json:"days"`
	From         string           `json:"from"`
	To           string           `json:"to"`
	Series       []DayPoint       `json:"series"`
	Current      PeriodTotals     `json:"current"`
	Previous     PeriodTotals     `json:"previous"`
	Statuses     []StatusCount    `json:"statuses"`
	TopMerchants []MerchantVolume `json:"top_merchants"`
}

// Analytics summarises the last `days` IST calendar days (7, 30 or 90),
// with the preceding period of equal length for comparison.
func (s *Service) Analytics(ctx context.Context, days int) (Analytics, error) {
	switch days {
	case 7, 30, 90:
	default:
		days = 30
	}
	now := time.Now().In(IST)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, IST)
	start := today.AddDate(0, 0, -(days - 1))
	prevStart := start.AddDate(0, 0, -days)

	rows, err := s.q.DailyPaymentStats(ctx, store.DailyPaymentStatsParams{FirstDay: prevStart, LastDay: today})
	if err != nil {
		return Analytics{}, err
	}
	out := Analytics{Days: days, From: start.Format(time.DateOnly), To: today.Format(time.DateOnly), Series: make([]DayPoint, 0, days)}
	for i, r := range rows {
		p := DayPoint{
			Day: r.Day.Format(time.DateOnly), CapturedVolume: r.CapturedVolume, Succeeded: r.Succeeded,
			Failed: r.Failed, Total: r.Total, RefundedVolume: r.RefundedVolume, Fees: r.Fees,
		}
		if i < len(rows)-days {
			add(&out.Previous, p)
		} else {
			add(&out.Current, p)
			out.Series = append(out.Series, p)
		}
	}
	finish(&out.Current)
	finish(&out.Previous)

	statuses, err := s.q.StatusBreakdown(ctx, start)
	if err != nil {
		return Analytics{}, err
	}
	out.Statuses = make([]StatusCount, 0, len(statuses))
	for _, st := range statuses {
		out.Statuses = append(out.Statuses, StatusCount{Status: st.Status, Count: st.N, Amount: st.Amount})
	}
	top, err := s.q.TopMerchants(ctx, store.TopMerchantsParams{Since: start, Lim: 5})
	if err != nil {
		return Analytics{}, err
	}
	out.TopMerchants = make([]MerchantVolume, 0, len(top))
	for _, m := range top {
		out.TopMerchants = append(out.TopMerchants, MerchantVolume{ID: m.ID, Name: m.Name, CapturedVolume: m.CapturedVolume, Succeeded: m.Succeeded})
	}
	return out, nil
}

func add(t *PeriodTotals, p DayPoint) {
	t.CapturedVolume += p.CapturedVolume
	t.Succeeded += p.Succeeded
	t.Failed += p.Failed
	t.Total += p.Total
	t.RefundedVolume += p.RefundedVolume
	t.Fees += p.Fees
}

func finish(t *PeriodTotals) {
	if n := t.Succeeded + t.Failed; n > 0 {
		t.SuccessRate = float64(t.Succeeded) / float64(n)
	}
	if t.Succeeded > 0 {
		t.AvgTicket = t.CapturedVolume / t.Succeeded
	}
}

type SearchResults struct {
	Payments  []Payment  `json:"payments"`
	Merchants []Merchant `json:"merchants"`
}

// Search finds payments by ID prefix, provider order/payment ID or
// description, and merchants by ID prefix or name.
func (s *Service) Search(ctx context.Context, q string) (SearchResults, error) {
	q = strings.TrimSpace(q)
	out := SearchResults{Payments: []Payment{}, Merchants: []Merchant{}}
	if len(q) < 2 || len(q) > 100 {
		return out, nil
	}
	prefix := strings.ToLower(q)
	if strings.Trim(prefix, "0123456789abcdef-") != "" {
		prefix = "~" // not an ID: no UUID starts with "~", so the prefix matches nothing
	}
	payments, err := s.q.SearchPayments(ctx, store.SearchPaymentsParams{Prefix: &prefix, Q: &q})
	if err != nil {
		return out, err
	}
	for _, p := range payments {
		out.Payments = append(out.Payments, paymentFromRow(p))
	}
	merchants, err := s.q.SearchMerchants(ctx, store.SearchMerchantsParams{Prefix: &prefix, Q: q})
	if err != nil {
		return out, err
	}
	for _, m := range merchants {
		out.Merchants = append(out.Merchants, merchantFromRow(m))
	}
	return out, nil
}
