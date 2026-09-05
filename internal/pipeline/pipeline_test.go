package pipeline

import (
	"context"
	"testing"
)

// TestRun_RealFixturesWithoutJudge exercises the full pipeline against
// the real Phase 0 fixtures with no AI judge configured (Config.Judge
// nil) — the honest "no Vertex AI credentials" mode, which must still
// produce a complete, sensible report rather than failing.
func TestRun_RealFixturesWithoutJudge(t *testing.T) {
	report, err := Run(context.Background(), Config{FixturesDir: "../../data/fixtures"})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if report.GeneratedAt.IsZero() {
		t.Error("GeneratedAt is zero")
	}
	if len(report.Results) != 52 {
		t.Errorf("len(Results) = %d, want 52", len(report.Results))
	}
	if report.Verdicts != nil {
		t.Errorf("Verdicts = %v, want nil (no judge configured)", report.Verdicts)
	}
	// 1 refund (FEE_MISMATCH) + 1 missing-bank (MISSING_IN_BANK) + 2 duplicate (POSSIBLE_DUPLICATE, pending)
	if len(report.Exceptions) != 4 {
		t.Errorf("len(Exceptions) = %d, want 4", len(report.Exceptions))
	}
	if report.Summary.TotalOrders != 52 {
		t.Errorf("Summary.TotalOrders = %d, want 52", report.Summary.TotalOrders)
	}
	if report.Summary.MatchedCount != 48 {
		t.Errorf("Summary.MatchedCount = %d, want 48", report.Summary.MatchedCount)
	}
	if report.Summary.GroundTruthAccuracy == nil {
		t.Error("Summary.GroundTruthAccuracy is nil, want a value (ground_truth.json exists in the fixtures dir)")
	}
}

func TestRun_MissingFixturesDirReturnsError(t *testing.T) {
	_, err := Run(context.Background(), Config{FixturesDir: "../../data/does-not-exist"})
	if err == nil {
		t.Fatal("expected an error for a missing fixtures directory, got nil")
	}
}
