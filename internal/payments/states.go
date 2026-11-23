package payments

type Status string

const (
	StatusCreated           Status = "created"
	StatusAuthorized        Status = "authorized"
	StatusCaptured          Status = "captured"
	StatusPartiallyRefunded Status = "partially_refunded"
	StatusRefunded          Status = "refunded"
	StatusCanceled          Status = "canceled"
	StatusFailed            Status = "failed"
)

const (
	PayoutPending = "pending"
	PayoutPaid    = "paid"
	PayoutFailed  = "failed"
)

//	created ──► authorized ──► captured ──► partially_refunded ──► refunded
//	   │            │              └───────────────────────────────────▲
//	   ▼            ▼
//	 failed      canceled
//
// Must match the status_transitions table; TestTransitionsMatchDatabase
// enforces that.
//
// The backward edges from refunded/partially_refunded happen only when the
// provider reports that a refund failed.
var paymentTransitions = map[Status][]Status{
	StatusCreated:           {StatusAuthorized, StatusFailed},
	StatusAuthorized:        {StatusCaptured, StatusCanceled},
	StatusCaptured:          {StatusPartiallyRefunded, StatusRefunded},
	StatusPartiallyRefunded: {StatusPartiallyRefunded, StatusRefunded, StatusCaptured},
	StatusRefunded:          {StatusPartiallyRefunded, StatusCaptured},
}

var payoutTransitions = map[string][]string{
	PayoutPending: {PayoutPaid, PayoutFailed},
}

var refundTransitions = map[string][]string{
	"pending": {"succeeded", "failed"},
}

func CanTransition(from, to Status) bool {
	for _, s := range paymentTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

func canTransitionPayout(from, to string) bool {
	for _, s := range payoutTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}
