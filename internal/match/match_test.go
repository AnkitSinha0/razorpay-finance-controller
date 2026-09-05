package match

import (
	"testing"
	"time"

	"razorpay_finance_controller/internal/ingest"
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

func ledger(orderID string, gross int64) models.Transaction {
	return models.Transaction{Source: models.SourceLedger, OrderID: orderID, GrossAmount: gross, Status: "PAID"}
}

func gateway(orderID, paymentID, settlementID string, gross, fee, tax int64, date time.Time) models.Transaction {
	return models.Transaction{
		Source: models.SourceGateway, OrderID: orderID, PaymentID: paymentID,
		SettlementID: settlementID, GrossAmount: gross, Fee: fee, Tax: tax,
		NetAmount: gross - fee - tax, Date: date,
	}
}

func bank(utr string, credit int64, date time.Time, description string) models.Transaction {
	return models.Transaction{Source: models.SourceBank, UTR: utr, CreditAmount: credit, Date: date, Description: description}
}

func resultFor(t *testing.T, results []Result, orderID string) Result {
	t.Helper()
	for _, r := range results {
		if r.OrderID == orderID {
			return r
		}
	}
	t.Fatalf("no result for order %s", orderID)
	return Result{}
}

func TestRun_ExactReferenceMatch(t *testing.T) {
	d := mustDate(t, "2026-09-01")
	batch := ingest.Batch{
		Ledger:  []models.Transaction{ledger("ORD_1", 1000000)},
		Gateway: []models.Transaction{gateway("ORD_1", "PAY_1", "ST_1", 1000000, 18000, 3240, d)},
		Bank:    []models.Transaction{bank("UTR_1", 978760, d, "RAZORPAY PAY_1")},
	}
	r := resultFor(t, Run(batch), "ORD_1")

	if r.Decision != Matched || r.Rule != RuleExactReference {
		t.Fatalf("got Decision=%s Rule=%s, want MATCHED/EXACT_REFERENCE", r.Decision, r.Rule)
	}
	if r.Delta != 0 {
		t.Errorf("Delta = %d, want 0", r.Delta)
	}
}

func TestRun_ReferenceMatchWithRefundIsDiscrepancy(t *testing.T) {
	d := mustDate(t, "2026-09-01")
	// gross-fee-tax = 1000000-18000-3240 = 978760 (plain expected net).
	// Actual bank credit is lower because of a refund the engine never sees.
	batch := ingest.Batch{
		Ledger:  []models.Transaction{ledger("ORD_1", 1000000)},
		Gateway: []models.Transaction{gateway("ORD_1", "PAY_1", "ST_1", 1000000, 18000, 3240, d)},
		Bank:    []models.Transaction{bank("UTR_1", 928760, d, "RAZORPAY PAY_1")},
	}
	r := resultFor(t, Run(batch), "ORD_1")

	if r.Decision != Discrepancy || r.Rule != RuleExactReference {
		t.Fatalf("got Decision=%s Rule=%s, want DISCREPANCY/EXACT_REFERENCE", r.Decision, r.Rule)
	}
	if r.Delta != -50000 {
		t.Errorf("Delta = %d, want -50000", r.Delta)
	}
}

func TestRun_DateWindowMatch(t *testing.T) {
	settleDate := mustDate(t, "2026-09-01")
	creditDate := mustDate(t, "2026-09-03") // 2 days later, inside the 3-day window
	batch := ingest.Batch{
		Ledger:  []models.Transaction{ledger("ORD_1", 500000)},
		Gateway: []models.Transaction{gateway("ORD_1", "PAY_1", "ST_1", 500000, 9000, 1620, settleDate)},
		// generic description, no payment_id — forces the date-window rule
		Bank: []models.Transaction{bank("UTR_1", 489380, creditDate, "RAZORPAY SETTLEMENT")},
	}
	r := resultFor(t, Run(batch), "ORD_1")

	if r.Decision != Matched || r.Rule != RuleDateWindow {
		t.Fatalf("got Decision=%s Rule=%s, want MATCHED/DATE_WINDOW", r.Decision, r.Rule)
	}
}

func TestRun_OutsideDateWindowStaysUnresolved(t *testing.T) {
	settleDate := mustDate(t, "2026-09-01")
	creditDate := mustDate(t, "2026-09-06") // 5 days later, outside the 3-day window
	batch := ingest.Batch{
		Ledger:  []models.Transaction{ledger("ORD_1", 500000)},
		Gateway: []models.Transaction{gateway("ORD_1", "PAY_1", "ST_1", 500000, 9000, 1620, settleDate)},
		Bank:    []models.Transaction{bank("UTR_1", 489380, creditDate, "RAZORPAY SETTLEMENT")},
	}
	r := resultFor(t, Run(batch), "ORD_1")

	if r.Decision != Unresolved {
		t.Fatalf("got Decision=%s, want UNRESOLVED (credit is outside the date window)", r.Decision)
	}
}

func TestRun_AmbiguousDuplicateNeverAutoMatched(t *testing.T) {
	d := mustDate(t, "2026-09-01")
	batch := ingest.Batch{
		Ledger: []models.Transaction{ledger("ORD_1", 500000), ledger("ORD_2", 500000)},
		Gateway: []models.Transaction{
			gateway("ORD_1", "PAY_1", "ST_1", 500000, 9000, 1620, d),
			gateway("ORD_2", "PAY_2", "ST_2", 500000, 9000, 1620, d),
		},
		// two identical, generic-description credits — nothing ties either to a specific order
		Bank: []models.Transaction{
			bank("UTR_1", 489380, d, "RAZORPAY SETTLEMENT"),
			bank("UTR_2", 489380, d, "RAZORPAY SETTLEMENT"),
		},
	}
	results := Run(batch)
	r1 := resultFor(t, results, "ORD_1")
	r2 := resultFor(t, results, "ORD_2")

	for _, r := range []Result{r1, r2} {
		if r.Decision != Ambiguous {
			t.Errorf("%s: Decision = %s, want AMBIGUOUS", r.OrderID, r.Decision)
		}
		if len(r.CandidateUTRs) != 2 {
			t.Errorf("%s: CandidateUTRs = %v, want both UTRs listed", r.OrderID, r.CandidateUTRs)
		}
		if len(r.Candidates) != 2 {
			t.Fatalf("%s: Candidates = %v, want both scored", r.OrderID, r.Candidates)
		}
		// Both bank records are genuinely identical (same amount, date,
		// description) — an honest evidence score ties exactly here
		// rather than fabricating a distinguishing signal.
		if r.Candidates[0].Score != r.Candidates[1].Score {
			t.Errorf("%s: Candidates scores = %v, want tied for identical evidence", r.OrderID, r.Candidates)
		}
		// Priority 4: the cascade should narrate exactly what happened —
		// EXACT_REFERENCE found nothing, DATE_WINDOW escalated.
		if len(r.RuleCascade) != 2 {
			t.Fatalf("%s: RuleCascade = %+v, want 2 entries (EXACT_REFERENCE, DATE_WINDOW)", r.OrderID, r.RuleCascade)
		}
		if r.RuleCascade[0].Rule != RuleExactReference || r.RuleCascade[0].Outcome != "no_match" {
			t.Errorf("%s: RuleCascade[0] = %+v, want EXACT_REFERENCE/no_match", r.OrderID, r.RuleCascade[0])
		}
		if r.RuleCascade[1].Rule != RuleDateWindow || r.RuleCascade[1].Outcome != "escalated" {
			t.Errorf("%s: RuleCascade[1] = %+v, want DATE_WINDOW/escalated", r.OrderID, r.RuleCascade[1])
		}
	}
}

func TestRun_RuleCascade_ExactReferenceResolves(t *testing.T) {
	d := mustDate(t, "2026-09-01")
	batch := ingest.Batch{
		Ledger:  []models.Transaction{ledger("ORD_1", 500000)},
		Gateway: []models.Transaction{gateway("ORD_1", "PAY_1", "ST_1", 500000, 9000, 1620, d)},
		Bank:    []models.Transaction{bank("UTR_1", 489380, d, "RAZORPAY PAY_1")},
	}
	r := resultFor(t, Run(batch), "ORD_1")
	if len(r.RuleCascade) != 1 || r.RuleCascade[0].Rule != RuleExactReference || r.RuleCascade[0].Outcome != "resolved" {
		t.Errorf("RuleCascade = %+v, want a single resolved EXACT_REFERENCE entry", r.RuleCascade)
	}
}

func TestRun_BatchSumMatch(t *testing.T) {
	d := mustDate(t, "2026-09-01")
	batch := ingest.Batch{
		Ledger: []models.Transaction{ledger("ORD_1", 500000), ledger("ORD_2", 600000), ledger("ORD_3", 700000)},
		Gateway: []models.Transaction{
			gateway("ORD_1", "PAY_1", "ST_BATCH", 500000, 9000, 1620, d),
			gateway("ORD_2", "PAY_2", "ST_BATCH", 600000, 10800, 1944, d),
			gateway("ORD_3", "PAY_3", "ST_BATCH", 700000, 12600, 2268, d),
		},
		// one bank credit for the whole settlement, sum of the three plain nets
		Bank: []models.Transaction{bank("UTR_BATCH", 489380+587256+685132, d, "RAZORPAY SETTLEMENT ST_BATCH")},
	}
	results := Run(batch)

	for _, orderID := range []string{"ORD_1", "ORD_2", "ORD_3"} {
		r := resultFor(t, results, orderID)
		if r.Decision != Matched || r.Rule != RuleBatchSum {
			t.Errorf("%s: got Decision=%s Rule=%s, want MATCHED/BATCH_SUM", orderID, r.Decision, r.Rule)
		}
		if len(r.UTRs) != 1 || r.UTRs[0] != "UTR_BATCH" {
			t.Errorf("%s: UTRs = %v, want [UTR_BATCH]", orderID, r.UTRs)
		}
	}
}

func TestRun_MissingBankEvidenceIsUnresolved(t *testing.T) {
	d := mustDate(t, "2026-09-01")
	batch := ingest.Batch{
		Ledger:  []models.Transaction{ledger("ORD_1", 500000)},
		Gateway: []models.Transaction{gateway("ORD_1", "PAY_1", "ST_1", 500000, 9000, 1620, d)},
		Bank:    nil,
	}
	r := resultFor(t, Run(batch), "ORD_1")

	if r.Decision != Unresolved {
		t.Fatalf("got Decision=%s, want UNRESOLVED", r.Decision)
	}
}

func TestRun_MissingGatewayRecordIsUnresolved(t *testing.T) {
	batch := ingest.Batch{
		Ledger:  []models.Transaction{ledger("ORD_1", 500000)},
		Gateway: nil,
		Bank:    nil,
	}
	r := resultFor(t, Run(batch), "ORD_1")

	if r.Decision != Unresolved {
		t.Fatalf("got Decision=%s, want UNRESOLVED", r.Decision)
	}
}

// TestRun_RealFixtures cross-checks the engine against Phase 0's
// ground_truth.json categories: 48 orders should resolve cleanly
// (NORMAL + DATE_LAG + BATCH), the refund order should be a
// DISCREPANCY, the duplicate pair AMBIGUOUS, and the missing-bank order
// UNRESOLVED.
func TestRun_RealFixtures(t *testing.T) {
	batch, err := ingest.LoadAll("../../data/fixtures")
	if err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	results := Run(batch)

	counts := map[Decision]int{}
	for _, r := range results {
		counts[r.Decision]++
	}

	want := map[Decision]int{Matched: 48, Discrepancy: 1, Ambiguous: 2, Unresolved: 1}
	for decision, wantCount := range want {
		if counts[decision] != wantCount {
			t.Errorf("count[%s] = %d, want %d", decision, counts[decision], wantCount)
		}
	}

	if r := resultFor(t, results, "ORD_1049"); r.Decision != Discrepancy {
		t.Errorf("ORD_1049 (refund) Decision = %s, want DISCREPANCY", r.Decision)
	}
	if r := resultFor(t, results, "ORD_1050"); r.Decision != Ambiguous {
		t.Errorf("ORD_1050 (duplicate) Decision = %s, want AMBIGUOUS", r.Decision)
	} else {
		// The fixture's two candidate bank credits are deliberately
		// identical (same amount, date, description) — the evidence
		// score should honestly tie rather than invent a preference.
		if len(r.Candidates) != 2 || r.Candidates[0].Score != r.Candidates[1].Score {
			t.Errorf("ORD_1050 Candidates = %v, want two tied evidence scores", r.Candidates)
		}
	}
	if r := resultFor(t, results, "ORD_1051"); r.Decision != Ambiguous {
		t.Errorf("ORD_1051 (duplicate) Decision = %s, want AMBIGUOUS", r.Decision)
	}
	if r := resultFor(t, results, "ORD_1052"); r.Decision != Unresolved {
		t.Errorf("ORD_1052 (missing bank) Decision = %s, want UNRESOLVED", r.Decision)
	}
}
