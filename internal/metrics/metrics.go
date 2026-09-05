// Package metrics computes match rate, value-reconciled %, AI
// escalation rate, and ground-truth accuracy for a reconciliation run.
// CLAUDE.md's core lesson for this phase: a high match rate is not
// automatically a good result, so these four numbers are always
// reported together — none of them alone tells the full story. See
// CLAUDE.md Phase 5.
package metrics

import (
	"razorpay_finance_controller/internal/judge"
	"razorpay_finance_controller/internal/match"
)

// Summary is the full-batch scorecard for one reconciliation run.
type Summary struct {
	TotalOrders         int     `json:"total_orders"`
	MatchedCount        int     `json:"matched_count"`
	DiscrepancyCount    int     `json:"discrepancy_count"`
	UnresolvedCount     int     `json:"unresolved_count"`
	PendingAICount      int     `json:"pending_ai_count"` // AMBIGUOUS orders with no AI verdict yet (judge not run, or skipped)
	MatchRate           float64 `json:"match_rate"`
	ValueReconciledRate float64 `json:"value_reconciled_rate"`
	AIEscalationRate    float64 `json:"ai_escalation_rate"`
	// GroundTruthAccuracy is nil when no ground truth was supplied
	// (the normal case in production — this is a synthetic-data-only
	// evaluation aid). GroundTruthEvaluated/-Total make clear how many
	// orders the accuracy figure actually covers: a pending (still-
	// AMBIGUOUS) order can't be scored right or wrong yet, so it's
	// excluded from the numerator and denominator rather than counted
	// as a miss.
	GroundTruthAccuracy  *float64 `json:"ground_truth_accuracy,omitempty"`
	GroundTruthEvaluated int      `json:"ground_truth_evaluated,omitempty"`
	GroundTruthTotal     int      `json:"ground_truth_total,omitempty"`
	// GroundTruthMisses lists the orders whose terminal decision
	// disagreed with ground truth — the actual, named errors behind an
	// accuracy figure below 100%. Empty when accuracy is perfect or no
	// ground truth was supplied. A miss where the engine said MATCHED is
	// the dangerous kind: it raises no exception, so only this scoring
	// surfaces it.
	GroundTruthMisses []GroundTruthMiss `json:"ground_truth_misses,omitempty"`
}

// GroundTruthMiss is one order the pipeline classified differently from
// the known-correct answer.
type GroundTruthMiss struct {
	OrderID  string `json:"order_id"`
	Expected string `json:"expected"`
	Got      string `json:"got"`
}

// Compute builds the Summary from Phase 2's match.Results, Phase 3's
// judge.Verdicts (nil if the judge hasn't run), and optional ground
// truth (nil in production).
func Compute(results []match.Result, verdicts []judge.Verdict, groundTruth []GroundTruthEntry) Summary {
	verdictByOrder := make(map[string]judge.Verdict, len(verdicts))
	for _, v := range verdicts {
		verdictByOrder[v.OrderID] = v
	}

	var s Summary
	s.TotalOrders = len(results)

	var totalExpected, totalMatchedValue int64
	var escalated int
	finalByOrder := make(map[string]string, len(results))

	for _, r := range results {
		v, hasVerdict := verdictByOrder[r.OrderID]
		final := finalDecision(r, v, hasVerdict)
		finalByOrder[r.OrderID] = final
		totalExpected += r.ExpectedNet

		if r.Decision == match.Ambiguous {
			escalated++
		}

		switch final {
		case string(match.Matched):
			s.MatchedCount++
			totalMatchedValue += r.ExpectedNet
		case string(match.Discrepancy):
			s.DiscrepancyCount++
		case string(match.Unresolved):
			s.UnresolvedCount++
		case string(match.Ambiguous):
			s.PendingAICount++
		}
	}

	s.MatchRate = percent(int64(s.MatchedCount), int64(s.TotalOrders))
	s.ValueReconciledRate = percent(totalMatchedValue, totalExpected)
	s.AIEscalationRate = percent(int64(escalated), int64(s.TotalOrders))

	if len(groundTruth) > 0 {
		s.GroundTruthTotal = len(groundTruth)
		var correct int
		for _, g := range groundTruth {
			final, ok := finalByOrder[g.OrderID]
			if !ok || final == string(match.Ambiguous) {
				continue // no terminal decision yet — don't score it either way
			}
			s.GroundTruthEvaluated++
			if final == g.ExpectedDecision {
				correct++
			} else {
				s.GroundTruthMisses = append(s.GroundTruthMisses, GroundTruthMiss{
					OrderID: g.OrderID, Expected: g.ExpectedDecision, Got: final,
				})
			}
		}
		if s.GroundTruthEvaluated > 0 {
			acc := percent(int64(correct), int64(s.GroundTruthEvaluated))
			s.GroundTruthAccuracy = &acc
		}
	}

	return s
}

// finalDecision resolves an order's terminal decision, applying the AI
// judge's verdict to an AMBIGUOUS match.Result when one is available.
// Mirrors the same merge exceptions.Build performs internally; kept
// separate (not shared) since each is a small, single-use helper local
// to its own package's concerns.
func finalDecision(r match.Result, v judge.Verdict, hasVerdict bool) string {
	if r.Decision != match.Ambiguous {
		return string(r.Decision)
	}
	if !hasVerdict {
		return string(match.Ambiguous)
	}
	return string(v.Decision)
}

func percent(part, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}
