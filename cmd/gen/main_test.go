package main

import "testing"

func TestGenerate_RecordCounts(t *testing.T) {
	ledger, gateway, bank, ground := generate()

	if got := len(ledger); got < 50 {
		t.Errorf("ledger records = %d, want >= 50", got)
	}
	if len(gateway) != len(ledger) {
		t.Errorf("gateway records = %d, want %d (one per ledger order)", len(gateway), len(ledger))
	}
	if len(ground) != len(ledger) {
		t.Errorf("ground truth entries = %d, want %d (one per ledger order)", len(ground), len(ledger))
	}
	// bank has one fewer record per BATCH order beyond the first (3 orders
	// collapse into 1 bank credit) and one fewer for the MISSING_BANK order.
	if got, want := len(bank), len(ledger)-2-1; got != want {
		t.Errorf("bank records = %d, want %d", got, want)
	}
}

func TestGenerate_CaseTypeCoverage(t *testing.T) {
	_, _, _, ground := generate()

	counts := map[string]int{}
	for _, g := range ground {
		counts[g.CaseType]++
	}

	want := map[string]int{
		"NORMAL":       40,
		"DATE_LAG":     5,
		"BATCH":        3,
		"REFUND":       1,
		"DUPLICATE":    2,
		"MISSING_BANK": 1,
	}
	for caseType, wantCount := range want {
		if counts[caseType] != wantCount {
			t.Errorf("case_type %s count = %d, want %d", caseType, counts[caseType], wantCount)
		}
	}

	broken := len(ground) - counts["NORMAL"]
	pct := float64(broken) / float64(len(ground)) * 100
	if pct < 15 || pct > 30 {
		t.Errorf("non-NORMAL share = %.1f%%, want between 15%% and 30%%", pct)
	}
}

func TestGenerate_MoneyIsConsistent(t *testing.T) {
	ledger, gateway, bank, _ := generate()

	ledgerGross := map[string]int64{}
	for _, l := range ledger {
		ledgerGross[l.OrderID] = l.GrossAmount
	}

	for _, g := range gateway {
		if g.GrossAmount != ledgerGross[g.OrderID] {
			t.Errorf("%s: gateway gross %d != ledger gross %d", g.OrderID, g.GrossAmount, ledgerGross[g.OrderID])
		}
		if want := g.GrossAmount - g.Fee - g.Tax - g.RefundAmount; g.NetAmount != want {
			t.Errorf("%s: net_amount %d != gross-fee-tax-refund %d", g.OrderID, g.NetAmount, want)
		}
	}

	// batch settlement ST_546: bank credit must equal the sum of its 3 net amounts.
	var batchNetSum int64
	for _, g := range gateway {
		if g.SettlementID == "ST_546" {
			batchNetSum += g.NetAmount
		}
	}
	found := false
	for _, b := range bank {
		if b.Description == "RAZORPAY SETTLEMENT ST_546" {
			found = true
			if b.CreditAmount != batchNetSum {
				t.Errorf("batch bank credit %d != sum of batched net amounts %d", b.CreditAmount, batchNetSum)
			}
		}
	}
	if !found {
		t.Error("expected a batch settlement bank record for ST_546")
	}
}

func TestGenerate_Deterministic(t *testing.T) {
	l1, g1, b1, gt1 := generate()
	l2, g2, b2, gt2 := generate()

	if len(l1) != len(l2) || len(g1) != len(g2) || len(b1) != len(b2) || len(gt1) != len(gt2) {
		t.Fatal("generate() produced different record counts across runs")
	}
	for i := range l1 {
		if l1[i] != l2[i] {
			t.Errorf("ledger[%d] differs across runs: %+v vs %+v", i, l1[i], l2[i])
		}
	}
}
