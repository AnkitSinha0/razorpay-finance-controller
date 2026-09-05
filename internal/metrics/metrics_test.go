package metrics

import (
	"testing"

	"razorpay_finance_controller/internal/ingest"
	"razorpay_finance_controller/internal/judge"
	"razorpay_finance_controller/internal/match"
)

func TestCompute_BasicRates(t *testing.T) {
	results := []match.Result{
		{OrderID: "ORD_1", Decision: match.Matched, ExpectedNet: 100000},
		{OrderID: "ORD_2", Decision: match.Matched, ExpectedNet: 200000},
		{OrderID: "ORD_3", Decision: match.Discrepancy, ExpectedNet: 300000},
		{OrderID: "ORD_4", Decision: match.Unresolved, ExpectedNet: 400000},
	}
	s := Compute(results, nil, nil)

	if s.TotalOrders != 4 {
		t.Errorf("TotalOrders = %d, want 4", s.TotalOrders)
	}
	if s.MatchedCount != 2 {
		t.Errorf("MatchedCount = %d, want 2", s.MatchedCount)
	}
	if s.MatchRate != 50 {
		t.Errorf("MatchRate = %v, want 50", s.MatchRate)
	}
	// value reconciled = (100000+200000) / (100000+200000+300000+400000) = 300000/1000000 = 30%
	if s.ValueReconciledRate != 30 {
		t.Errorf("ValueReconciledRate = %v, want 30", s.ValueReconciledRate)
	}
	if s.AIEscalationRate != 0 {
		t.Errorf("AIEscalationRate = %v, want 0 (no AMBIGUOUS orders)", s.AIEscalationRate)
	}
}

func TestCompute_AIEscalationRate(t *testing.T) {
	results := []match.Result{
		{OrderID: "ORD_1", Decision: match.Matched, ExpectedNet: 100000},
		{OrderID: "ORD_2", Decision: match.Ambiguous, ExpectedNet: 100000, CandidateUTRs: []string{"U1", "U2"}},
		{OrderID: "ORD_3", Decision: match.Ambiguous, ExpectedNet: 100000, CandidateUTRs: []string{"U3", "U4"}},
		{OrderID: "ORD_4", Decision: match.Matched, ExpectedNet: 100000},
	}
	s := Compute(results, nil, nil)

	if s.AIEscalationRate != 50 {
		t.Errorf("AIEscalationRate = %v, want 50 (2 of 4 orders were AMBIGUOUS)", s.AIEscalationRate)
	}
	if s.PendingAICount != 2 {
		t.Errorf("PendingAICount = %d, want 2 (no verdicts supplied)", s.PendingAICount)
	}
}

func TestCompute_AmbiguousResolvedByVerdictCountsAsFinal(t *testing.T) {
	results := []match.Result{
		{OrderID: "ORD_1", Decision: match.Ambiguous, ExpectedNet: 100000, CandidateUTRs: []string{"U1", "U2"}},
	}
	verdicts := []judge.Verdict{
		{OrderID: "ORD_1", Decision: judge.Matched, MatchedIDs: []string{"U1"}},
	}
	s := Compute(results, verdicts, nil)

	if s.MatchedCount != 1 {
		t.Errorf("MatchedCount = %d, want 1 (AI resolved the ambiguity to MATCHED)", s.MatchedCount)
	}
	if s.PendingAICount != 0 {
		t.Errorf("PendingAICount = %d, want 0", s.PendingAICount)
	}
	// still counts toward AI escalation rate — it *was* sent to the judge
	if s.AIEscalationRate != 100 {
		t.Errorf("AIEscalationRate = %v, want 100", s.AIEscalationRate)
	}
}

func TestCompute_GroundTruthAccuracy(t *testing.T) {
	results := []match.Result{
		{OrderID: "ORD_1", Decision: match.Matched, ExpectedNet: 100000},
		{OrderID: "ORD_2", Decision: match.Discrepancy, ExpectedNet: 100000}, // wrong vs ground truth (expects MATCHED)
		{OrderID: "ORD_3", Decision: match.Ambiguous, ExpectedNet: 100000, CandidateUTRs: []string{"U1", "U2"}},
	}
	groundTruth := []GroundTruthEntry{
		{OrderID: "ORD_1", ExpectedDecision: "MATCHED"},
		{OrderID: "ORD_2", ExpectedDecision: "MATCHED"},
		{OrderID: "ORD_3", ExpectedDecision: "UNRESOLVED"}, // still pending, no verdict — must not be scored
	}
	s := Compute(results, nil, groundTruth)

	if s.GroundTruthTotal != 3 {
		t.Errorf("GroundTruthTotal = %d, want 3", s.GroundTruthTotal)
	}
	if s.GroundTruthEvaluated != 2 {
		t.Fatalf("GroundTruthEvaluated = %d, want 2 (ORD_3 is still pending, excluded)", s.GroundTruthEvaluated)
	}
	if s.GroundTruthAccuracy == nil || *s.GroundTruthAccuracy != 50 {
		t.Fatalf("GroundTruthAccuracy = %v, want 50 (1 of 2 evaluated orders correct)", s.GroundTruthAccuracy)
	}
	if len(s.GroundTruthMisses) != 1 || s.GroundTruthMisses[0].OrderID != "ORD_2" ||
		s.GroundTruthMisses[0].Expected != "MATCHED" || s.GroundTruthMisses[0].Got != "DISCREPANCY" {
		t.Errorf("GroundTruthMisses = %+v, want one entry {ORD_2 MATCHED DISCREPANCY}", s.GroundTruthMisses)
	}
}

func TestCompute_NoGroundTruthLeavesAccuracyNil(t *testing.T) {
	results := []match.Result{{OrderID: "ORD_1", Decision: match.Matched, ExpectedNet: 100000}}
	s := Compute(results, nil, nil)

	if s.GroundTruthAccuracy != nil {
		t.Errorf("GroundTruthAccuracy = %v, want nil", s.GroundTruthAccuracy)
	}
}

func TestCompute_EmptyBatchDoesNotDivideByZero(t *testing.T) {
	s := Compute(nil, nil, nil)
	if s.MatchRate != 0 || s.ValueReconciledRate != 0 || s.AIEscalationRate != 0 {
		t.Errorf("got %+v, want all rates 0 for an empty batch", s)
	}
}

// TestCompute_RealFixtures cross-checks against the real Phase 0-2
// data: 48 of 52 orders MATCHED (40 NORMAL + 5 DATE_LAG + 3 BATCH), 1
// DISCREPANCY, 2 still-AMBIGUOUS (AI escalation, no verdicts supplied
// here), 1 UNRESOLVED.
func TestCompute_RealFixtures(t *testing.T) {
	batch, err := ingest.LoadAll("../../data/fixtures")
	if err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	groundTruth, err := LoadGroundTruth("../../data/fixtures/ground_truth.json")
	if err != nil {
		t.Fatalf("LoadGroundTruth failed: %v", err)
	}
	results := match.Run(batch)
	s := Compute(results, nil, groundTruth)

	if s.TotalOrders != 52 {
		t.Errorf("TotalOrders = %d, want 52", s.TotalOrders)
	}
	if s.MatchedCount != 48 {
		t.Errorf("MatchedCount = %d, want 48", s.MatchedCount)
	}
	if s.DiscrepancyCount != 1 {
		t.Errorf("DiscrepancyCount = %d, want 1", s.DiscrepancyCount)
	}
	if s.UnresolvedCount != 1 {
		t.Errorf("UnresolvedCount = %d, want 1", s.UnresolvedCount)
	}
	if s.PendingAICount != 2 {
		t.Errorf("PendingAICount = %d, want 2 (the DUPLICATE pair, no judge run)", s.PendingAICount)
	}
	wantEscalation := float64(2) / float64(52) * 100
	if s.AIEscalationRate != wantEscalation {
		t.Errorf("AIEscalationRate = %v, want %v", s.AIEscalationRate, wantEscalation)
	}
	// 50 of 52 orders have a terminal decision (the pending pair is excluded);
	// all 50 should match ground truth exactly.
	if s.GroundTruthEvaluated != 50 {
		t.Errorf("GroundTruthEvaluated = %d, want 50", s.GroundTruthEvaluated)
	}
	if s.GroundTruthAccuracy == nil || *s.GroundTruthAccuracy != 100 {
		t.Errorf("GroundTruthAccuracy = %v, want 100", s.GroundTruthAccuracy)
	}
}
