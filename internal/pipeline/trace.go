package pipeline

import (
	"razorpay_finance_controller/internal/exceptions"
	"razorpay_finance_controller/internal/judge"
	"razorpay_finance_controller/internal/match"
)

// OrderTrace is the combined, read-only view of everything one
// reconciliation run computed for a single order: the deterministic
// match.Result (rule, candidates, evidence scores — Phase 9), the
// judge.Verdict if the order was escalated and judged, and the
// exceptions.Exception if it didn't cleanly resolve to MATCHED. Verdict
// and Exception are nil when no such record exists for this order — a
// clean MATCHED has neither, a DISCREPANCY resolved deterministically
// has an Exception but no Verdict (CLAUDE.md Phase 10).
type OrderTrace struct {
	OrderID   string                `json:"order_id"`
	Match     match.Result          `json:"match"`
	Verdict   *judge.Verdict        `json:"verdict,omitempty"`
	Exception *exceptions.Exception `json:"exception,omitempty"`
}

// Trace stitches together the already-cached Report's three per-order
// records for orderID. It performs no new pipeline run and touches no
// new storage — everything it returns was computed by Run and already
// lives on Report. The second return value is false when orderID
// doesn't appear in Report.Results at all.
func (r Report) Trace(orderID string) (OrderTrace, bool) {
	var mr match.Result
	found := false
	for _, res := range r.Results {
		if res.OrderID == orderID {
			mr = res
			found = true
			break
		}
	}
	if !found {
		return OrderTrace{}, false
	}

	t := OrderTrace{OrderID: orderID, Match: mr}
	for _, v := range r.Verdicts {
		if v.OrderID == orderID {
			verdict := v
			t.Verdict = &verdict
			break
		}
	}
	for _, e := range r.Exceptions {
		if e.OrderID == orderID {
			exc := e
			t.Exception = &exc
			break
		}
	}
	return t, true
}
