package exceptions

import (
	"testing"
	"time"

	"razorpay_finance_controller/internal/ingest"
	"razorpay_finance_controller/internal/judge"
	"razorpay_finance_controller/internal/match"
	"razorpay_finance_controller/internal/models"
)

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestBuild_DiscrepancyIsFeeMismatch(t *testing.T) {
	results := []match.Result{
		{OrderID: "ORD_1", Decision: match.Discrepancy, ExpectedNet: 500000, ActualCredit: 450000, Delta: -50000, Notes: "refund"},
	}
	out := Build(results, nil, nil, nil, time.Now())

	if len(out) != 1 {
		t.Fatalf("got %d exceptions, want 1", len(out))
	}
	e := out[0]
	if e.Category != FeeMismatch || e.Action != VerifyFee {
		t.Errorf("got Category=%s Action=%s, want FEE_MISMATCH/VERIFY_FEE", e.Category, e.Action)
	}
	if e.ValueAtRisk != 50000 {
		t.Errorf("ValueAtRisk = %d, want 50000 (the delta, not the full expected net)", e.ValueAtRisk)
	}
	if e.Source != Deterministic {
		t.Errorf("Source = %s, want DETERMINISTIC", e.Source)
	}
}

func TestBuild_RecentUnresolvedIsMissingInBank(t *testing.T) {
	asOf := mustDate(t, "2026-09-10")
	settleDate := mustDate(t, "2026-09-05") // 5 days before asOf, well under the 14-day stale threshold
	results := []match.Result{
		{OrderID: "ORD_1", Decision: match.Unresolved, ExpectedNet: 500000, Notes: "no bank credit found"},
	}
	gateway := []models.Transaction{{OrderID: "ORD_1", Date: settleDate}}

	out := Build(results, nil, gateway, nil, asOf)

	if len(out) != 1 || out[0].Category != MissingInBank || out[0].Action != Escalate {
		t.Fatalf("got %+v, want one MISSING_IN_BANK/ESCALATE exception", out)
	}
}

func TestBuild_OldUnresolvedIsStaleSettlement(t *testing.T) {
	asOf := mustDate(t, "2026-09-30")
	settleDate := mustDate(t, "2026-09-01") // 29 days before asOf, past the 14-day stale threshold
	results := []match.Result{
		{OrderID: "ORD_1", Decision: match.Unresolved, ExpectedNet: 500000, Notes: "no bank credit found"},
	}
	gateway := []models.Transaction{{OrderID: "ORD_1", Date: settleDate}}

	out := Build(results, nil, gateway, nil, asOf)

	if len(out) != 1 || out[0].Category != StaleSettlement || out[0].Action != Recheck {
		t.Fatalf("got %+v, want one STALE_SETTLEMENT/RECHECK exception", out)
	}
}

func TestBuild_AmbiguousWithoutVerdictIsPendingDuplicate(t *testing.T) {
	results := []match.Result{
		{OrderID: "ORD_1", Decision: match.Ambiguous, ExpectedNet: 500000, CandidateUTRs: []string{"U1", "U2"}},
	}
	out := Build(results, nil, nil, nil, time.Now())

	if len(out) != 1 || out[0].Category != PossibleDuplicate || out[0].Action != ManualReview {
		t.Fatalf("got %+v, want one POSSIBLE_DUPLICATE/MANUAL_REVIEW exception", out)
	}
	if out[0].Source != Deterministic {
		t.Errorf("Source = %s, want DETERMINISTIC (no verdict yet)", out[0].Source)
	}
}

func TestBuild_AmbiguousResolvedUnresolvedByAIIsDuplicate(t *testing.T) {
	results := []match.Result{
		{OrderID: "ORD_1", Decision: match.Ambiguous, ExpectedNet: 500000, CandidateUTRs: []string{"U1", "U2"}},
	}
	verdicts := []judge.Verdict{
		{OrderID: "ORD_1", Decision: judge.Unresolved, Reason: "no distinguishing evidence", Confidence: 0.8, RiskIfWrong: "MEDIUM"},
	}
	out := Build(results, verdicts, nil, nil, time.Now())

	if len(out) != 1 {
		t.Fatalf("got %d exceptions, want 1", len(out))
	}
	e := out[0]
	if e.Category != PossibleDuplicate || e.Source != AIJudge {
		t.Errorf("got Category=%s Source=%s, want POSSIBLE_DUPLICATE/AI_JUDGE", e.Category, e.Source)
	}
	if e.Confidence != 0.8 || e.RiskIfWrong != "MEDIUM" {
		t.Errorf("Confidence/RiskIfWrong not carried through: %+v", e)
	}
}

func TestBuild_AmbiguousResolvedMatchedByAIIsNotAnException(t *testing.T) {
	results := []match.Result{
		{OrderID: "ORD_1", Decision: match.Ambiguous, ExpectedNet: 500000, CandidateUTRs: []string{"U1", "U2"}},
	}
	verdicts := []judge.Verdict{
		{OrderID: "ORD_1", Decision: judge.Matched, MatchedIDs: []string{"U1"}, Reason: "found a decisive signal", Confidence: 0.9, RiskIfWrong: "LOW"},
	}
	out := Build(results, verdicts, nil, nil, time.Now())

	if len(out) != 0 {
		t.Fatalf("got %d exceptions, want 0 (AI resolved it to MATCHED)", len(out))
	}
}

func TestBuild_MatchedIsNeverAnException(t *testing.T) {
	results := []match.Result{{OrderID: "ORD_1", Decision: match.Matched}}
	out := Build(results, nil, nil, nil, time.Now())
	if len(out) != 0 {
		t.Fatalf("got %d exceptions, want 0", len(out))
	}
}

func TestBuild_RankedByValueAtRiskNotOrder(t *testing.T) {
	results := []match.Result{
		{OrderID: "ORD_SMALL", Decision: match.Unresolved, ExpectedNet: 10000},
		{OrderID: "ORD_BIG", Decision: match.Unresolved, ExpectedNet: 5000000},
		{OrderID: "ORD_MID", Decision: match.Unresolved, ExpectedNet: 200000},
	}
	out := Build(results, nil, nil, nil, time.Now())

	if len(out) != 3 {
		t.Fatalf("got %d exceptions, want 3", len(out))
	}
	if out[0].OrderID != "ORD_BIG" || out[1].OrderID != "ORD_MID" || out[2].OrderID != "ORD_SMALL" {
		t.Errorf("not ranked by value at risk descending: %v, %v, %v", out[0].OrderID, out[1].OrderID, out[2].OrderID)
	}
}

// TestBuild_RealFixtures cross-checks against Phase 0-2's real data:
// the refund order should be FEE_MISMATCH, the missing-bank order
// MISSING_IN_BANK, and the duplicate pair POSSIBLE_DUPLICATE (pending,
// since no judge verdicts are supplied here).
func TestBuild_RealFixtures(t *testing.T) {
	batch, err := ingest.LoadAll("../../data/fixtures")
	if err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	results := match.Run(batch)
	// asOf is shortly after the batch's latest settlement date (2026-10-11
	// for the MISSING_BANK fixture order) — a realistic "ran reconciliation
	// a few days after month-end" scenario, well under the 14-day stale
	// threshold, so the missing-bank order stays MISSING_IN_BANK rather
	// than aging into STALE_SETTLEMENT.
	out := Build(results, nil, batch.Gateway, batch.Ledger, mustDate(t, "2026-10-15"))

	byOrder := make(map[string]Exception, len(out))
	for _, e := range out {
		byOrder[e.OrderID] = e
	}

	if len(out) != 4 {
		t.Fatalf("got %d exceptions, want 4 (1 refund + 1 missing-bank + 2 duplicate)", len(out))
	}
	if e, ok := byOrder["ORD_1049"]; !ok || e.Category != FeeMismatch {
		t.Errorf("ORD_1049 (refund): got %+v, want FEE_MISMATCH", e)
	}
	if e, ok := byOrder["ORD_1052"]; !ok || e.Category != MissingInBank {
		t.Errorf("ORD_1052 (missing bank): got %+v, want MISSING_IN_BANK", e)
	}
	if e, ok := byOrder["ORD_1050"]; !ok || e.Category != PossibleDuplicate {
		t.Errorf("ORD_1050 (duplicate): got %+v, want POSSIBLE_DUPLICATE", e)
	}
	if e, ok := byOrder["ORD_1051"]; !ok || e.Category != PossibleDuplicate {
		t.Errorf("ORD_1051 (duplicate): got %+v, want POSSIBLE_DUPLICATE", e)
	}

	// every exception must have an action assigned
	for _, e := range out {
		if e.Action == "" {
			t.Errorf("%s: Action is empty", e.OrderID)
		}
	}
}
