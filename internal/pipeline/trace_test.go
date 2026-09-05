package pipeline

import (
	"context"
	"testing"

	"razorpay_finance_controller/internal/judge"
)

// stubJudge is a minimal judge.Provider (CLAUDE.md Implementation Rule
// 4: provider isolated behind an interface) that lets this test exercise
// the AI-judge leg of Trace without live Vertex AI credentials.
type stubJudge struct{}

func (stubJudge) Judge(ctx context.Context, req judge.Request) (judge.Verdict, error) {
	return judge.Verdict{
		OrderID:     req.OrderID,
		Decision:    judge.Unresolved,
		Reason:      "stub: no distinguishing evidence between tied candidates",
		Confidence:  0.5,
		RiskIfWrong: "MEDIUM",
	}, nil
}

func TestTrace_UnknownOrderNotFound(t *testing.T) {
	report, err := Run(context.Background(), Config{FixturesDir: "../../data/fixtures"})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if _, found := report.Trace("ORD_DOES_NOT_EXIST"); found {
		t.Error("Trace found a result for an order that isn't in the batch")
	}
}

// TestTrace_RefundOrder exercises match + exception with no judge
// verdict: ORD_1049 is a DISCREPANCY resolved entirely by the
// deterministic engine (a reference match ties it to one bank credit,
// but the plain fee/tax formula doesn't reconcile because of a partial
// refund) — it's never AMBIGUOUS, so the judge never sees it.
func TestTrace_RefundOrder(t *testing.T) {
	report, err := Run(context.Background(), Config{FixturesDir: "../../data/fixtures"})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	trace, found := report.Trace("ORD_1049")
	if !found {
		t.Fatal("ORD_1049 not found")
	}
	if trace.Match.OrderID != "ORD_1049" {
		t.Errorf("Match.OrderID = %q, want ORD_1049", trace.Match.OrderID)
	}
	if trace.Verdict != nil {
		t.Errorf("Verdict = %+v, want nil (never escalated to the AI judge)", trace.Verdict)
	}
	if trace.Exception == nil {
		t.Fatal("Exception = nil, want a FEE_MISMATCH exception")
	}
	if trace.Exception.Category != "FEE_MISMATCH" {
		t.Errorf("Exception.Category = %s, want FEE_MISMATCH", trace.Exception.Category)
	}
}

// TestTrace_DuplicateOrderWithJudge exercises all three legs: match,
// judge verdict, and exception, for one of the DUPLICATE-pair orders —
// using a stub judge so the AI leg is genuinely populated without live
// credentials.
func TestTrace_DuplicateOrderWithJudge(t *testing.T) {
	report, err := Run(context.Background(), Config{FixturesDir: "../../data/fixtures", Judge: stubJudge{}})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	trace, found := report.Trace("ORD_1050")
	if !found {
		t.Fatal("ORD_1050 not found")
	}
	if trace.Match.Decision != "AMBIGUOUS" {
		t.Errorf("Match.Decision = %s, want AMBIGUOUS", trace.Match.Decision)
	}
	if len(trace.Match.Candidates) != 2 {
		t.Errorf("Match.Candidates = %v, want 2 scored candidates", trace.Match.Candidates)
	}
	if trace.Verdict == nil {
		t.Fatal("Verdict = nil, want the stub judge's verdict")
	}
	if trace.Verdict.Decision != judge.Unresolved {
		t.Errorf("Verdict.Decision = %s, want UNRESOLVED", trace.Verdict.Decision)
	}
	if trace.Exception == nil {
		t.Fatal("Exception = nil, want a POSSIBLE_DUPLICATE exception")
	}
	if trace.Exception.Category != "POSSIBLE_DUPLICATE" {
		t.Errorf("Exception.Category = %s, want POSSIBLE_DUPLICATE", trace.Exception.Category)
	}
	if trace.Exception.Source != "AI_JUDGE" {
		t.Errorf("Exception.Source = %s, want AI_JUDGE", trace.Exception.Source)
	}
}
