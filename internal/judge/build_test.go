package judge

import (
	"testing"

	"razorpay_finance_controller/internal/ingest"
	"razorpay_finance_controller/internal/match"
	"razorpay_finance_controller/internal/models"
)

func TestBuildRequests_OnlyAmbiguousOrders(t *testing.T) {
	batch, err := ingest.LoadAll("../../data/fixtures")
	if err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	results := match.Run(batch)

	reqs := BuildRequests(results, batch.Bank)

	if len(reqs) != 2 {
		t.Fatalf("got %d requests, want 2 (the DUPLICATE pair)", len(reqs))
	}
	for _, req := range reqs {
		if len(req.Candidates) != 2 {
			t.Errorf("%s: got %d candidates, want 2", req.OrderID, len(req.Candidates))
		}
		for _, c := range req.Candidates {
			if c.CreditAmount == 0 || c.Date == "" || c.Description == "" {
				t.Errorf("%s: candidate %+v missing detail from the bank record", req.OrderID, c)
			}
		}
	}
}

// TestBuildRequests_EvidenceScoreSurvives confirms Phase 9's match-time
// evidence score is carried onto each Candidate unchanged, keyed by UTR
// (not just position) so a reordering of CandidateUTRs can't silently
// mismatch a score to the wrong candidate.
func TestBuildRequests_EvidenceScoreSurvives(t *testing.T) {
	results := []match.Result{
		{
			OrderID:       "ORD_1",
			Decision:      match.Ambiguous,
			ExpectedNet:   489380,
			CandidateUTRs: []string{"UTR_A", "UTR_B"},
			Candidates: []match.CandidateEvidence{
				{UTR: "UTR_A", Score: 80},
				{UTR: "UTR_B", Score: 55},
			},
		},
	}
	bank := []models.Transaction{
		{Source: models.SourceBank, UTR: "UTR_A", CreditAmount: 489380, Description: "RAZORPAY SETTLEMENT"},
		{Source: models.SourceBank, UTR: "UTR_B", CreditAmount: 489380, Description: "RAZORPAY SETTLEMENT"},
	}

	reqs := BuildRequests(results, bank)
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}

	scoreByUTR := make(map[string]int)
	for _, c := range reqs[0].Candidates {
		scoreByUTR[c.UTR] = c.EvidenceScore
	}
	if scoreByUTR["UTR_A"] != 80 {
		t.Errorf("UTR_A EvidenceScore = %d, want 80", scoreByUTR["UTR_A"])
	}
	if scoreByUTR["UTR_B"] != 55 {
		t.Errorf("UTR_B EvidenceScore = %d, want 55", scoreByUTR["UTR_B"])
	}
}
