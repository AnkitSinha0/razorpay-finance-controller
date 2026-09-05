package judge

import (
	"razorpay_finance_controller/internal/match"
	"razorpay_finance_controller/internal/models"
)

const dateLayout = "2006-01-02"

// BuildRequests converts every AMBIGUOUS match.Result into a judge
// Request, attaching the full bank-record detail (amount, date,
// description) for each tied candidate UTR so the judge has real
// evidence to reason over, not just an ID.
func BuildRequests(results []match.Result, bank []models.Transaction) []Request {
	bankByUTR := make(map[string]models.Transaction, len(bank))
	for _, b := range bank {
		bankByUTR[b.UTR] = b
	}

	var reqs []Request
	for _, r := range results {
		if r.Decision != match.Ambiguous {
			continue
		}
		scoreByUTR := make(map[string]int, len(r.Candidates))
		for _, c := range r.Candidates {
			scoreByUTR[c.UTR] = c.Score
		}
		req := Request{
			OrderID:     r.OrderID,
			ExpectedNet: r.ExpectedNet,
		}
		if len(r.PaymentIDs) > 0 {
			req.PaymentID = r.PaymentIDs[0]
		}
		if len(r.SettlementIDs) > 0 {
			req.SettlementID = r.SettlementIDs[0]
		}
		for _, utr := range r.CandidateUTRs {
			b, ok := bankByUTR[utr]
			if !ok {
				continue
			}
			req.Candidates = append(req.Candidates, Candidate{
				UTR:           b.UTR,
				CreditAmount:  b.CreditAmount,
				Date:          b.Date.Format(dateLayout),
				Description:   b.Description,
				EvidenceScore: scoreByUTR[b.UTR],
			})
		}
		reqs = append(reqs, req)
	}
	return reqs
}
