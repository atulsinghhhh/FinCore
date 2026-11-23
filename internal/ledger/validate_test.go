package ledger

import (
	"math"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestValidatePostRequest(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	valid := func() PostRequest {
		return PostRequest{
			IdempotencyKey: "k1",
			Description:    "payment",
			Postings: []PostingInput{
				{AccountID: a, Direction: Debit, Amount: 100000, Currency: "INR"},
				{AccountID: b, Direction: Credit, Amount: 98000, Currency: "INR"},
				{AccountID: c, Direction: Credit, Amount: 2000, Currency: "INR"},
			},
		}
	}

	tests := []struct {
		name   string
		mutate func(*PostRequest)
		want   error
	}{
		{"valid split payment", func(*PostRequest) {}, nil},
		{"missing idempotency key", func(r *PostRequest) { r.IdempotencyKey = "" }, ErrInvalidRequest},
		{"missing description", func(r *PostRequest) { r.Description = "" }, ErrInvalidRequest},
		{"single posting", func(r *PostRequest) { r.Postings = r.Postings[:1] }, ErrInvalidRequest},
		{"zero amount", func(r *PostRequest) { r.Postings[0].Amount = 0 }, ErrInvalidRequest},
		{"negative amount", func(r *PostRequest) { r.Postings[1].Amount = -98000 }, ErrInvalidRequest},
		{"unknown direction", func(r *PostRequest) { r.Postings[0].Direction = "sideways" }, ErrInvalidRequest},
		{"lowercase currency", func(r *PostRequest) { r.Postings[0].Currency = "inr" }, ErrInvalidRequest},
		{"missing account", func(r *PostRequest) { r.Postings[0].AccountID = uuid.Nil }, ErrInvalidRequest},
		{"debits exceed credits", func(r *PostRequest) { r.Postings[0].Amount = 100001 }, ErrUnbalanced},
		{"balanced total but split across currencies", func(r *PostRequest) { r.Postings[2].Currency = "USD" }, ErrUnbalanced},
		{"overflow", func(r *PostRequest) {
			r.Postings = []PostingInput{
				{AccountID: a, Direction: Debit, Amount: math.MaxInt64, Currency: "INR"},
				{AccountID: b, Direction: Debit, Amount: 1, Currency: "INR"},
			}
		}, ErrInvalidRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := valid()
			tt.mutate(&r)
			err := validatePostRequest(r)
			if tt.want == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.want)
			}
		})
	}
}

func TestNormalBalance(t *testing.T) {
	require.Equal(t, Debit, Asset.NormalBalance())
	require.Equal(t, Debit, Expense.NormalBalance())
	require.Equal(t, Credit, Liability.NormalBalance())
	require.Equal(t, Credit, Equity.NormalBalance())
	require.Equal(t, Credit, Revenue.NormalBalance())
}
