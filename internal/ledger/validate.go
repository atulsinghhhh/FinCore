package ledger

import (
	"fmt"
	"math"
	"regexp"

	"github.com/google/uuid"
)

var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

func ValidCurrency(c string) bool { return currencyPattern.MatchString(c) }

func validateNewAccount(a NewAccount) error {
	switch {
	case a.Code == "":
		return fmt.Errorf("%w: account code is required", ErrInvalidRequest)
	case a.Name == "":
		return fmt.Errorf("%w: account name is required", ErrInvalidRequest)
	case !a.Type.Valid():
		return fmt.Errorf("%w: unknown account type %q", ErrInvalidRequest, a.Type)
	case !ValidCurrency(a.Currency):
		return fmt.Errorf("%w: currency %q must be a 3-letter ISO 4217 code", ErrInvalidRequest, a.Currency)
	}
	return nil
}

// validatePostRequest checks everything that can be checked without the
// database: shape, positive amounts, and debits = credits per currency.
// The database re-checks balance at commit, so this exists to give callers
// precise errors, not as the only line of defence.
func validatePostRequest(r PostRequest) error {
	if r.IdempotencyKey == "" {
		return fmt.Errorf("%w: idempotency key is required", ErrInvalidRequest)
	}
	if r.Description == "" {
		return fmt.Errorf("%w: description is required", ErrInvalidRequest)
	}
	if len(r.Postings) < 2 {
		return fmt.Errorf("%w: a transaction needs at least 2 postings, got %d", ErrInvalidRequest, len(r.Postings))
	}

	net := map[string]int64{}
	for i, p := range r.Postings {
		switch {
		case p.AccountID == uuid.Nil:
			return fmt.Errorf("%w: posting %d has no account", ErrInvalidRequest, i)
		case !p.Direction.Valid():
			return fmt.Errorf("%w: posting %d has unknown direction %q", ErrInvalidRequest, i, p.Direction)
		case p.Amount <= 0:
			return fmt.Errorf("%w: posting %d amount must be positive, got %d", ErrInvalidRequest, i, p.Amount)
		case !ValidCurrency(p.Currency):
			return fmt.Errorf("%w: posting %d currency %q is invalid", ErrInvalidRequest, i, p.Currency)
		}

		signed := p.Amount
		if p.Direction == Credit {
			signed = -p.Amount
		}
		sum, ok := addChecked(net[p.Currency], signed)
		if !ok {
			return fmt.Errorf("%w: posting amounts overflow", ErrInvalidRequest)
		}
		net[p.Currency] = sum
	}

	for currency, n := range net {
		if n != 0 {
			return fmt.Errorf("%w: %s is off by %d minor units", ErrUnbalanced, currency, n)
		}
	}
	return nil
}

func addChecked(a, b int64) (int64, bool) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, false
	}
	return a + b, true
}
