package pipeline

import "time"

// Resolution is one human decision logged against an order — the seed
// of a feedback loop (CLAUDE.md Phase 12). It only records what a human
// decided and why; it never mutates the order's match.Result or
// exceptions.Exception, and it never moves money — this is an
// append-only audit log, not an action.
//
// UTR is which specific tied candidate (if any) the human approved —
// on an AMBIGUOUS order with multiple candidates, "approved" is only a
// meaningful decision once it names which one (Precision &
// Impressiveness Pass, Priority 5). It's empty for actions that aren't
// about a specific candidate (e.g. a catch-all "flag for review").
type Resolution struct {
	OrderID   string    `json:"order_id"`
	Action    string    `json:"action"`
	UTR       string    `json:"utr,omitempty"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}
