// Package exceptions classifies every non-MATCHED order
// (MISSING_IN_BANK, POSSIBLE_DUPLICATE, FEE_MISMATCH, STALE_SETTLEMENT),
// assigns a recommended action (RECHECK, MANUAL_REVIEW, VERIFY_FEE,
// ESCALATE), and ranks results by ₹ value at risk — not record count,
// per CLAUDE.md's core lesson that a wrong high-value match is worse
// than an unresolved one. See CLAUDE.md Phase 4.
package exceptions

import (
	"sort"
	"time"

	"razorpay_finance_controller/internal/judge"
	"razorpay_finance_controller/internal/match"
	"razorpay_finance_controller/internal/models"
)

// Category is the diagnosis for why an order didn't cleanly reconcile.
type Category string

const (
	MissingInBank     Category = "MISSING_IN_BANK"
	PossibleDuplicate Category = "POSSIBLE_DUPLICATE"
	FeeMismatch       Category = "FEE_MISMATCH"
	StaleSettlement   Category = "STALE_SETTLEMENT"
)

// Action is the recommended next step for a human operator.
type Action string

const (
	Recheck      Action = "RECHECK"
	ManualReview Action = "MANUAL_REVIEW"
	VerifyFee    Action = "VERIFY_FEE"
	Escalate     Action = "ESCALATE"
)

// categoryAction is the fixed Category -> Action mapping. Business
// rules for "what to do about it" live here, deliberately separate from
// how results get presented (dashboard/API, later phases).
var categoryAction = map[Category]Action{
	MissingInBank:     Escalate,
	PossibleDuplicate: ManualReview,
	FeeMismatch:       VerifyFee,
	StaleSettlement:   Recheck,
}

// Source records whether the final decision came from the deterministic
// engine alone or required the AI judge.
type Source string

const (
	Deterministic Source = "DETERMINISTIC"
	AIJudge       Source = "AI_JUDGE"
)

// staleThreshold: a settlement with zero bank evidence after this long
// is flagged STALE_SETTLEMENT (money is very likely stuck, not just
// running a day or two behind) rather than the more provisional
// MISSING_IN_BANK.
const staleThreshold = 14 * 24 * time.Hour

// Exception is one order that did not cleanly resolve to MATCHED.
type Exception struct {
	OrderID string `json:"order_id"`
	// FinalDecision is the terminal match.Decision/judge.Decision label
	// (DISCREPANCY, UNRESOLVED) — never AMBIGUOUS, which always gets
	// resolved one way or another before reaching this struct.
	FinalDecision string   `json:"final_decision"`
	Category      Category `json:"category"`
	Action        Action   `json:"action"`
	// ValueAtRisk is the ₹ amount (paise) this exception should be
	// ranked by. For a discrepancy where a reference match already ties
	// the order to a specific bank credit, only the unexplained delta is
	// genuinely uncertain; everywhere else the full expected amount is
	// unaccounted for.
	ValueAtRisk int64  `json:"value_at_risk"`
	Source      Source `json:"source"`
	// Confidence and RiskIfWrong are populated only for AI_JUDGE
	// exceptions; zero/empty for deterministic ones (the deterministic
	// engine doesn't estimate confidence — its rules are exact or they
	// don't fire).
	Confidence  float64 `json:"confidence,omitempty"`
	RiskIfWrong string  `json:"risk_if_wrong,omitempty"`
	Reason      string  `json:"reason,omitempty"`
}

// Build classifies every non-MATCHED order. gateway and ledger are the
// normalized source records (for settlement-date and fallback-amount
// lookups); verdicts are the AI judge's answers for whichever AMBIGUOUS
// orders were escalated in Phase 3 — pass nil if the judge hasn't run,
// and those orders classify as POSSIBLE_DUPLICATE pending review. asOf
// is the reference date STALE_SETTLEMENT is measured against; callers
// choose it explicitly so classification stays reproducible (CLAUDE.md
// Implementation Rule 7) instead of depending on wall-clock time.
func Build(results []match.Result, verdicts []judge.Verdict, gateway, ledger []models.Transaction, asOf time.Time) []Exception {
	gatewayByOrder := make(map[string]models.Transaction, len(gateway))
	for _, g := range gateway {
		gatewayByOrder[g.OrderID] = g
	}
	ledgerGrossByOrder := make(map[string]int64, len(ledger))
	for _, l := range ledger {
		ledgerGrossByOrder[l.OrderID] = l.GrossAmount
	}
	verdictByOrder := make(map[string]judge.Verdict, len(verdicts))
	for _, v := range verdicts {
		verdictByOrder[v.OrderID] = v
	}

	var out []Exception
	for _, r := range results {
		if r.Decision == match.Matched {
			continue
		}

		exc, skip := classify(r, verdictByOrder[r.OrderID], gatewayByOrder[r.OrderID], asOf)
		if skip {
			continue // AI judge resolved this AMBIGUOUS case to MATCHED — no exception
		}
		if exc.ValueAtRisk == 0 {
			exc.ValueAtRisk = ledgerGrossByOrder[r.OrderID]
		}
		out = append(out, exc)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ValueAtRisk > out[j].ValueAtRisk })
	return out
}

func classify(r match.Result, v judge.Verdict, g models.Transaction, asOf time.Time) (Exception, bool) {
	base := Exception{OrderID: r.OrderID}

	switch r.Decision {
	case match.Discrepancy:
		base.Source = Deterministic
		base.FinalDecision = string(match.Discrepancy)
		base.Category = FeeMismatch
		base.Reason = r.Notes
		if r.Delta != 0 {
			base.ValueAtRisk = abs(r.Delta)
		} else {
			base.ValueAtRisk = r.ExpectedNet
		}

	case match.Unresolved:
		base.Source = Deterministic
		base.FinalDecision = string(match.Unresolved)
		base.Reason = r.Notes
		base.ValueAtRisk = r.ExpectedNet
		if !g.Date.IsZero() && asOf.Sub(g.Date) >= staleThreshold {
			base.Category = StaleSettlement
		} else {
			base.Category = MissingInBank
		}

	case match.Ambiguous:
		if v.OrderID == "" {
			// no verdict yet: judge hasn't run, or this order wasn't in
			// its output. Report honestly as still pending, not resolved.
			base.Source = Deterministic
			base.FinalDecision = string(match.Ambiguous)
			base.Category = PossibleDuplicate
			base.ValueAtRisk = r.ExpectedNet
			base.Reason = "multiple equally-plausible bank candidates; not yet reviewed by the AI judge"
			break
		}
		switch v.Decision {
		case judge.Matched:
			return Exception{}, true // AI found decisive evidence; cleanly resolved
		case judge.Discrepancy:
			base.Category = FeeMismatch
		case judge.Unresolved:
			base.Category = PossibleDuplicate
		}
		base.Source = AIJudge
		base.FinalDecision = string(v.Decision)
		base.ValueAtRisk = r.ExpectedNet
		base.Confidence = v.Confidence
		base.RiskIfWrong = v.RiskIfWrong
		base.Reason = v.Reason
	}

	base.Action = categoryAction[base.Category]
	return base, false
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
