// Package match implements the deterministic reconciliation engine:
// exact reference match, then fee/tax-adjusted amount + date-window
// matching, then N:1 settlement-batch sum matching. Zero API calls —
// anything left over is reported as AMBIGUOUS (for the Phase 3 Claude
// judge) or UNRESOLVED (no evidence exists at all, no judge needed).
// See CLAUDE.md Phase 2.
package match

import (
	"fmt"
	"math"
	"strings"
	"time"

	"razorpay_finance_controller/internal/ingest"
	"razorpay_finance_controller/internal/models"
)

// Decision is the deterministic engine's classification for one ledger
// order. AMBIGUOUS and UNRESOLVED are both "not matched", but for
// different reasons: AMBIGUOUS means multiple equally-plausible bank
// candidates exist (needs a judge — human or AI); UNRESOLVED means zero
// candidates exist (no judge can help, the evidence simply isn't there).
type Decision string

const (
	Matched     Decision = "MATCHED"
	Discrepancy Decision = "DISCREPANCY"
	Ambiguous   Decision = "AMBIGUOUS"
	Unresolved  Decision = "UNRESOLVED"
)

// Rule identifies which deterministic rule produced a Result.
type Rule string

const (
	RuleExactReference Rule = "EXACT_REFERENCE"
	RuleDateWindow     Rule = "DATE_WINDOW"
	RuleBatchSum       Rule = "BATCH_SUM"
	RuleNone           Rule = "NONE"
)

// dateWindow is the maximum settlement-to-credit lag the deterministic
// engine will accept as a date match, per CLAUDE.md's 1-3 day window.
const dateWindow = 3 * 24 * time.Hour

// Result is the deterministic engine's verdict for one ledger order.
type Result struct {
	OrderID       string   `json:"order_id"`
	Decision      Decision `json:"decision"`
	Rule          Rule     `json:"rule"`
	PaymentIDs    []string `json:"payment_ids,omitempty"`
	UTRs          []string `json:"utrs,omitempty"`
	SettlementIDs []string `json:"settlement_ids,omitempty"`
	// ExpectedNet is computed independently as gross-fee-tax, deliberately
	// ignoring RefundAmount even when the gateway record reports one. The
	// engine does not trust the vendor's net_amount at face value — if a
	// refund (or any other adjustment) makes the actual bank credit
	// diverge from this plain formula, that surfaces as a DISCREPANCY for
	// a human/AI to verify, rather than being silently absorbed.
	ExpectedNet int64 `json:"expected_net"`
	// ActualCredit is the matched bank credit amount (or sum, for a batch
	// match). Zero when no bank evidence was found at all.
	ActualCredit int64 `json:"actual_credit"`
	// Delta is ActualCredit - ExpectedNet. Zero for a clean MATCHED.
	Delta int64 `json:"delta"`
	// CandidateUTRs lists the tied bank candidates for an AMBIGUOUS
	// result — recorded, never guessed from.
	CandidateUTRs []string `json:"candidate_utrs,omitempty"`
	// Candidates carries the same tied UTRs as CandidateUTRs, each with
	// its computed evidence score (Phase 9). Populated alongside
	// CandidateUTRs, never as a separate pass over the data.
	Candidates []CandidateEvidence `json:"candidates,omitempty"`
	// DecisionConfidence is 0 for every non-AMBIGUOUS result (not
	// applicable) and, for AMBIGUOUS results, the margin between the top
	// two candidates' evidence scores scaled 0-100 — a distinct claim
	// from either candidate's own evidence_score: it measures how
	// clearly one candidate wins, not how strong either candidate looks
	// in isolation (Precision & Impressiveness Pass, Priority 2).
	DecisionConfidence float64 `json:"decision_confidence"`
	// RuleCascade narrates which rule ran at each pass, whether it
	// resolved the order, and why not when it didn't — built from data
	// Pass 1/2 already compute while deciding routing, surfaced as text
	// instead of only being used internally (Precision & Impressiveness
	// Pass, Priority 4: "why this reached AI review").
	RuleCascade []RuleAttempt `json:"rule_cascade,omitempty"`
	Notes       string        `json:"notes,omitempty"`
}

// RuleAttempt is one deterministic-engine pass's outcome for an order:
// which rule ran, whether it resolved the order outright, found nothing,
// or escalated it to the AI judge, and a short reason.
type RuleAttempt struct {
	Rule    Rule   `json:"rule"`
	Outcome string `json:"outcome"` // "resolved", "no_match", or "escalated"
	Detail  string `json:"detail"`
}

// plainNet is the deterministic engine's own fee/tax-adjusted net
// calculation. It intentionally does not use gateway.NetAmount or
// gateway.RefundAmount.
func plainNet(g models.Transaction) int64 {
	return g.GrossAmount - g.Fee - g.Tax
}

func withinWindow(a, b time.Time) bool {
	d := a.Sub(b)
	if d < 0 {
		d = -d
	}
	return d <= dateWindow
}

// bankPool is a consumable set of bank transactions: once a record is
// used by one match it cannot be used by another.
type bankPool struct {
	txns []models.Transaction
	used []bool
}

func newBankPool(txns []models.Transaction) *bankPool {
	return &bankPool{txns: txns, used: make([]bool, len(txns))}
}

func (p *bankPool) available() []int {
	var idx []int
	for i, u := range p.used {
		if !u {
			idx = append(idx, i)
		}
	}
	return idx
}

func (p *bankPool) consume(i int) { p.used[i] = true }

// Run executes the full deterministic pipeline over a normalized batch
// and returns one Result per ledger order, in ledger order.
func Run(batch ingest.Batch) []Result {
	gatewayByOrder := make(map[string]models.Transaction, len(batch.Gateway))
	for _, g := range batch.Gateway {
		gatewayByOrder[g.OrderID] = g
	}

	pool := newBankPool(batch.Bank)
	results := make(map[string]*Result, len(batch.Ledger))
	order := make([]string, 0, len(batch.Ledger))

	for _, l := range batch.Ledger {
		order = append(order, l.OrderID)
		g, ok := gatewayByOrder[l.OrderID]
		if !ok {
			results[l.OrderID] = &Result{
				OrderID: l.OrderID, Decision: Unresolved, Rule: RuleNone,
				Notes: "no gateway settlement record found for this order",
			}
			continue
		}
		results[l.OrderID] = &Result{
			OrderID: l.OrderID, PaymentIDs: []string{g.PaymentID},
			SettlementIDs: []string{g.SettlementID}, ExpectedNet: plainNet(g),
		}
	}

	// Pass 1: exact reference match — bank description contains the
	// gateway payment_id. Strongest evidence; resolves MATCHED or
	// DISCREPANCY (never AMBIGUOUS — a named reference is never tied).
	for _, orderID := range order {
		r := results[orderID]
		if r.Rule != "" || r.Decision == Unresolved {
			continue
		}
		g := gatewayByOrder[orderID]
		idx, found, refMatchCount := findSingleByReference(pool, g.PaymentID)
		switch {
		case found:
			b := pool.txns[idx]
			pool.consume(idx)
			finishSingleMatch(r, RuleExactReference, b)
			r.RuleCascade = append(r.RuleCascade, RuleAttempt{
				Rule: RuleExactReference, Outcome: "resolved",
				Detail: fmt.Sprintf("bank description contains payment reference %s", g.PaymentID),
			})
		case refMatchCount == 0:
			r.RuleCascade = append(r.RuleCascade, RuleAttempt{
				Rule: RuleExactReference, Outcome: "no_match",
				Detail: "no bank credit's description carries this order's payment reference",
			})
		default:
			r.RuleCascade = append(r.RuleCascade, RuleAttempt{
				Rule: RuleExactReference, Outcome: "no_match",
				Detail: fmt.Sprintf("%d bank credits reference this payment ID — ambiguous by reference alone, deferred to amount/date matching", refMatchCount),
			})
		}
	}

	// Pass 2: date-window amount match — no reference text, but exactly
	// one available bank record has the expected amount within the
	// settlement date window. Multiple equally-good candidates are left
	// AMBIGUOUS, not guessed at.
	for _, orderID := range order {
		r := results[orderID]
		if r.Rule != "" || r.Decision == Unresolved {
			continue
		}
		g := gatewayByOrder[orderID]
		candidates := findByAmountAndDate(pool, r.ExpectedNet, g.Date)
		switch len(candidates) {
		case 0:
			// leave for batch-sum pass
			r.RuleCascade = append(r.RuleCascade, RuleAttempt{
				Rule: RuleDateWindow, Outcome: "no_match",
				Detail: "no bank credit matches this order's amount within the settlement date window",
			})
		case 1:
			idx := candidates[0]
			b := pool.txns[idx]
			pool.consume(idx)
			finishSingleMatch(r, RuleDateWindow, b)
			r.RuleCascade = append(r.RuleCascade, RuleAttempt{
				Rule: RuleDateWindow, Outcome: "resolved",
				Detail: "exactly one bank credit matches this order's amount within the settlement date window",
			})
		default:
			r.Decision = Ambiguous
			r.Rule = RuleNone
			r.RuleCascade = append(r.RuleCascade, RuleAttempt{
				Rule: RuleDateWindow, Outcome: "escalated",
				Detail: fmt.Sprintf("%d candidates tied at the same amount and date window — escalated to AI judge", len(candidates)),
			})
			for _, idx := range candidates {
				b := pool.txns[idx]
				r.CandidateUTRs = append(r.CandidateUTRs, b.UTR)
				unique := descriptionIsUnique(pool.txns, candidates, idx)
				breakdown := evidenceBreakdown(b.CreditAmount-r.ExpectedNet, b.Date.Sub(g.Date), b.Description, g.PaymentID, unique)
				r.Candidates = append(r.Candidates, CandidateEvidence{
					UTR:       b.UTR,
					Score:     int(math.Round(breakdown.Total)),
					Breakdown: breakdown,
				})
			}
			totals := make([]float64, len(r.Candidates))
			for i, c := range r.Candidates {
				totals[i] = c.Breakdown.Total
			}
			r.DecisionConfidence = decisionConfidence(totals)
			r.Notes = "multiple bank credits match this order's amount and date window; cannot pick one without more evidence"
			if allCandidatesEvidentiallyIdentical(r.Candidates) {
				r.Notes += " Candidates are evidentially identical — no distinguishing signal exists in the available data."
			}
		}
	}

	// Pass 3: N:1 batch-sum match — group still-unresolved orders sharing
	// a settlement_id; if their combined expected net equals exactly one
	// available bank credit within the date window, they were settled
	// together.
	groups := make(map[string][]string) // settlement_id -> order_ids still pending
	for _, orderID := range order {
		r := results[orderID]
		if r.Rule != "" || r.Decision == Ambiguous || r.Decision == Unresolved {
			continue
		}
		g := gatewayByOrder[orderID]
		groups[g.SettlementID] = append(groups[g.SettlementID], orderID)
	}
	for settlementID, orderIDs := range groups {
		if len(orderIDs) < 2 {
			continue
		}
		var sum int64
		var anchorDate time.Time
		for i, orderID := range orderIDs {
			g := gatewayByOrder[orderID]
			sum += results[orderID].ExpectedNet
			if i == 0 {
				anchorDate = g.Date
			}
		}
		candidates := findByAmountAndDate(pool, sum, anchorDate)
		if len(candidates) != 1 {
			continue // ambiguous or no batch credit found; falls through to pass 4 / stays pass-2 style unresolved
		}
		idx := candidates[0]
		b := pool.txns[idx]
		pool.consume(idx)
		for _, orderID := range orderIDs {
			r := results[orderID]
			r.Decision = Matched
			r.Rule = RuleBatchSum
			r.UTRs = []string{b.UTR}
			r.ActualCredit = b.CreditAmount
			r.Delta = 0 // by construction the group sum equals the credit
			r.Notes = "settled together (settlement_id " + settlementID + ") with " + joinOrders(orderIDs, orderID)
		}
	}

	// Pass 4: whatever is left has zero bank evidence at all.
	for _, orderID := range order {
		r := results[orderID]
		if r.Rule == "" && r.Decision == "" {
			r.Decision = Unresolved
			r.Rule = RuleNone
			r.Notes = "no bank credit found matching this order's amount, reference, or settlement group"
		}
	}

	out := make([]Result, 0, len(order))
	for _, orderID := range order {
		out = append(out, *results[orderID])
	}
	return out
}

// findSingleByReference returns the one bank record whose description
// contains paymentID, if exactly one exists. matchCount is the total
// number of available records that reference paymentID at all (0, 1, or
// more), purely for narrating why this rule resolved, found nothing, or
// deferred (Priority 4's rule-cascade text) — it doesn't change the
// matching decision itself.
func findSingleByReference(pool *bankPool, paymentID string) (idx int, found bool, matchCount int) {
	var matches []int
	for _, idx := range pool.available() {
		if strings.Contains(pool.txns[idx].Description, paymentID) {
			matches = append(matches, idx)
		}
	}
	if len(matches) == 1 {
		return matches[0], true, 1
	}
	return -1, false, len(matches)
}

// descriptionIsUnique reports whether the candidate at idx has a
// description distinguishable from every other still-tied candidate for
// the same order. Two byte-identical descriptions (the DUPLICATE
// fixture case) correctly return false for both — there's genuinely
// nothing here to prefer one over the other.
func descriptionIsUnique(txns []models.Transaction, candidates []int, idx int) bool {
	desc := txns[idx].Description
	for _, other := range candidates {
		if other == idx {
			continue
		}
		if txns[other].Description == desc {
			return false
		}
	}
	return true
}

// allCandidatesEvidentiallyIdentical reports whether every tied
// candidate scored exactly the same evidence breakdown — meaning the
// underlying data (amount, date, description) offers no distinguishing
// signal at all between them, not just a coincidentally close score.
func allCandidatesEvidentiallyIdentical(candidates []CandidateEvidence) bool {
	if len(candidates) < 2 {
		return false
	}
	first := candidates[0].Breakdown
	for _, c := range candidates[1:] {
		if c.Breakdown != first {
			return false
		}
	}
	return true
}

func findByAmountAndDate(pool *bankPool, amount int64, anchor time.Time) []int {
	var found []int
	for _, idx := range pool.available() {
		b := pool.txns[idx]
		if b.CreditAmount == amount && withinWindow(b.Date, anchor) {
			found = append(found, idx)
		}
	}
	return found
}

func finishSingleMatch(r *Result, rule Rule, b models.Transaction) {
	r.Rule = rule
	r.UTRs = []string{b.UTR}
	r.ActualCredit = b.CreditAmount
	r.Delta = b.CreditAmount - r.ExpectedNet
	if r.Delta == 0 {
		r.Decision = Matched
	} else {
		r.Decision = Discrepancy
		r.Notes = "bank credit differs from the plain fee/tax-adjusted net; verify refund or fee adjustment"
	}
}

func joinOrders(all []string, exclude string) string {
	var parts []string
	for _, o := range all {
		if o != exclude {
			parts = append(parts, o)
		}
	}
	return strings.Join(parts, ", ")
}
