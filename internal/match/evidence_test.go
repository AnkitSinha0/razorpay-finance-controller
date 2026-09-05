package match

import (
	"testing"
	"time"
)

func TestEvidenceBreakdown(t *testing.T) {
	day := 24 * time.Hour

	cases := []struct {
		name        string
		amountDelta int64
		dateDelta   time.Duration
		description string
		paymentID   string
		unique      bool
		want        EvidenceBreakdown
	}{
		{
			"perfect match on every component", 0, 0, "RAZORPAY PAY_1050", "PAY_1050", true,
			EvidenceBreakdown{AmountScore: 40, DateScore: 15, ReferenceScore: 30, DescriptionScore: 10, UniquenessScore: 5, Total: 100},
		},
		{
			"generic settlement credit, no reference, not unique (DUPLICATE-shaped)", 0, day, "RAZORPAY SETTLEMENT", "PAY_1050", false,
			EvidenceBreakdown{AmountScore: 40, DateScore: 10, ReferenceScore: 0, DescriptionScore: 10, UniquenessScore: 0, Total: 60},
		},
		{
			"three days off zeroes the date component", 0, 3 * day, "SOME BANK TEXT", "", false,
			EvidenceBreakdown{AmountScore: 40, DateScore: 0, ReferenceScore: 0, DescriptionScore: 0, UniquenessScore: 0, Total: 40},
		},
		{
			"amount off by 10 rupees", 1000, 0, "RAZORPAY PAY_1050", "PAY_1050", true,
			EvidenceBreakdown{AmountScore: 30, DateScore: 15, ReferenceScore: 30, DescriptionScore: 10, UniquenessScore: 5, Total: 90},
		},
		{
			"negative amount delta treated the same as positive", -1000, 0, "RAZORPAY PAY_1050", "PAY_1050", true,
			EvidenceBreakdown{AmountScore: 30, DateScore: 15, ReferenceScore: 30, DescriptionScore: 10, UniquenessScore: 5, Total: 90},
		},
		{
			"everything wrong clamps at zero", 10000, 5 * day, "", "", false,
			EvidenceBreakdown{AmountScore: 0, DateScore: 0, ReferenceScore: 0, DescriptionScore: 0, UniquenessScore: 0, Total: 0},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evidenceBreakdown(tc.amountDelta, tc.dateDelta, tc.description, tc.paymentID, tc.unique)
			if got != tc.want {
				t.Errorf("evidenceBreakdown(...) = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestEvidenceBreakdown_DateScoreIsContinuous is Priority 3: date
// closeness must degrade smoothly within the tolerance window, not in
// whole-day steps, so two candidates a few hours apart resolve to
// genuinely different decimal totals rather than tying by coincidence.
func TestEvidenceBreakdown_DateScoreIsContinuous(t *testing.T) {
	sixHours := evidenceBreakdown(0, 6*time.Hour, "x", "", false).DateScore
	twelveHours := evidenceBreakdown(0, 12*time.Hour, "x", "", false).DateScore
	if sixHours <= twelveHours {
		t.Errorf("DateScore(6h)=%v should be strictly greater than DateScore(12h)=%v", sixHours, twelveHours)
	}
	if sixHours == MaxDateScore || sixHours == 0 {
		t.Errorf("DateScore(6h)=%v should be a genuine fraction of MaxDateScore, not a boundary value", sixHours)
	}
}

// TestEvidenceBreakdown_ReferenceSimilarityIsContinuous is Priority 3:
// a partial/near-miss reference should score between "exact" and
// "nothing", not collapse to the same 0 as an unrelated description.
func TestEvidenceBreakdown_ReferenceSimilarityIsContinuous(t *testing.T) {
	exact := evidenceBreakdown(0, 0, "RAZORPAY PAY_1050", "PAY_1050", false).ReferenceScore
	partial := evidenceBreakdown(0, 0, "RAZORPAY PAY_105", "PAY_1050", false).ReferenceScore
	none := evidenceBreakdown(0, 0, "RAZORPAY SETTLEMENT", "PAY_1050", false).ReferenceScore

	if exact != MaxReferenceScore {
		t.Errorf("exact reference ReferenceScore = %v, want %v", exact, MaxReferenceScore)
	}
	if !(partial > none && partial < exact) {
		t.Errorf("partial reference ReferenceScore = %v, want strictly between none=%v and exact=%v", partial, none, exact)
	}
}

// TestEvidenceScore_MatchesBreakdownTotal is Priority 1's core
// guarantee: the compact single-number score can never drift from the
// component breakdown, because it's computed from it.
func TestEvidenceScore_MatchesBreakdownTotal(t *testing.T) {
	day := 24 * time.Hour
	cases := []struct {
		amountDelta int64
		dateDelta   time.Duration
		description string
		paymentID   string
		unique      bool
	}{
		{0, 0, "RAZORPAY PAY_1050", "PAY_1050", true},
		{0, day, "RAZORPAY SETTLEMENT", "PAY_1050", false},
		{1000, 2 * day, "RAZORPAY PAY_1050", "PAY_1050", true},
		{50000, 10 * day, "", "", false},
	}

	for _, tc := range cases {
		breakdown := evidenceBreakdown(tc.amountDelta, tc.dateDelta, tc.description, tc.paymentID, tc.unique)
		score := evidenceScore(tc.amountDelta, tc.dateDelta, tc.description, tc.paymentID, tc.unique)
		wantScore := int(breakdown.Total + 0.5) // round-half-up, matches math.Round for non-negative totals
		if score != wantScore {
			t.Errorf("evidenceScore(%+v) = %d, want round(breakdown.Total=%v) = %d", tc, score, breakdown.Total, wantScore)
		}
	}
}

func TestEvidenceScore_ExactTiesScoreIdentically(t *testing.T) {
	a := evidenceScore(0, 24*time.Hour, "RAZORPAY SETTLEMENT", "PAY_1050", false)
	b := evidenceScore(0, 24*time.Hour, "RAZORPAY SETTLEMENT", "PAY_1050", false)
	if a != b {
		t.Errorf("identical evidence produced different scores: %d vs %d", a, b)
	}
}

func TestEvidenceScore_StrongerMatchScoresHigher(t *testing.T) {
	weak := evidenceScore(0, 3*24*time.Hour, "SOME BANK TEXT", "", false) // 3-day lag, no reference, no generic match
	strong := evidenceScore(0, 0, "RAZORPAY PAY_1050", "PAY_1050", true)  // same day, exact reference, distinguishable
	if strong <= weak {
		t.Errorf("stronger evidence (%d) did not outscore weaker evidence (%d)", strong, weak)
	}
}

// TestAllCandidatesEvidentiallyIdentical is Priority 3, item 2: when
// every input is truly identical, the totals should tie exactly (not a
// bug), and that state should be explicitly detectable rather than left
// as an unexplained coincidence.
func TestAllCandidatesEvidentiallyIdentical(t *testing.T) {
	identical := []CandidateEvidence{
		{UTR: "UTR_1", Breakdown: evidenceBreakdown(0, 24*time.Hour, "RAZORPAY SETTLEMENT", "PAY_1050", false)},
		{UTR: "UTR_2", Breakdown: evidenceBreakdown(0, 24*time.Hour, "RAZORPAY SETTLEMENT", "PAY_1050", false)},
	}
	if !allCandidatesEvidentiallyIdentical(identical) {
		t.Error("expected identical breakdowns to be detected as evidentially identical")
	}

	different := []CandidateEvidence{
		{UTR: "UTR_1", Breakdown: evidenceBreakdown(0, 0, "RAZORPAY PAY_1050", "PAY_1050", true)},
		{UTR: "UTR_2", Breakdown: evidenceBreakdown(0, 24*time.Hour, "RAZORPAY SETTLEMENT", "PAY_1050", false)},
	}
	if allCandidatesEvidentiallyIdentical(different) {
		t.Error("expected different breakdowns to not be flagged as identical")
	}

	if allCandidatesEvidentiallyIdentical([]CandidateEvidence{identical[0]}) {
		t.Error("a single candidate should never be flagged as 'identical to others'")
	}
}

// TestDecisionConfidence_WideMarginIsHigh and
// TestDecisionConfidence_NarrowMarginIsLow are Priority 2's spec cases
// verbatim: a wide margin between the top two candidates should read as
// high decision confidence, a narrow one (the kind that actually
// produces MANUAL_REVIEW) should read as low.
func TestDecisionConfidence_WideMarginIsHigh(t *testing.T) {
	got := decisionConfidence([]float64{40, 10})
	if got < 90 {
		t.Errorf("decisionConfidence(40, 10) = %v, want a high value (wide margin)", got)
	}
}

func TestDecisionConfidence_NarrowMarginIsLow(t *testing.T) {
	got := decisionConfidence([]float64{70.0, 69.4})
	if got > 20 {
		t.Errorf("decisionConfidence(70.0, 69.4) = %v, want a low value (narrow margin, matches MANUAL_REVIEW)", got)
	}
}

func TestDecisionConfidence_OrderOfInputsDoesNotMatter(t *testing.T) {
	a := decisionConfidence([]float64{70.0, 69.4})
	b := decisionConfidence([]float64{69.4, 70.0})
	if a != b {
		t.Errorf("decisionConfidence should be order-independent: %v vs %v", a, b)
	}
}

func TestDecisionConfidence_MoreThanTwoCandidatesUsesTopTwo(t *testing.T) {
	// a third, much weaker candidate must not distort the margin between
	// the two genuinely-tied leaders
	got := decisionConfidence([]float64{70.0, 69.4, 5.0})
	want := decisionConfidence([]float64{70.0, 69.4})
	if got != want {
		t.Errorf("decisionConfidence with a third weak candidate = %v, want %v (unaffected by it)", got, want)
	}
}
