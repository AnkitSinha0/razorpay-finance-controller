// Command gen produces the synthetic ledger/gateway/bank fixtures and
// their ground_truth.json under data/fixtures/. It is fully
// deterministic (no randomness) so the output is reproducible and the
// ground truth can be hand-verified. See CLAUDE.md Phase 0.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

type ledgerRecord struct {
	OrderID     string `json:"order_id"`
	Customer    string `json:"customer"`
	GrossAmount int64  `json:"gross_amount"`
	Status      string `json:"status"`
}

type gatewayRecord struct {
	PaymentID      string `json:"payment_id"`
	OrderID        string `json:"order_id"`
	GrossAmount    int64  `json:"gross_amount"`
	Fee            int64  `json:"fee"`
	Tax            int64  `json:"tax"`
	RefundAmount   int64  `json:"refund_amount,omitempty"`
	NetAmount      int64  `json:"net_amount"`
	SettlementID   string `json:"settlement_id"`
	SettlementDate string `json:"settlement_date"`
}

type bankRecord struct {
	UTR          string `json:"utr"`
	Date         string `json:"date"`
	CreditAmount int64  `json:"credit_amount"`
	Description  string `json:"description"`
}

type groundTruthEntry struct {
	OrderID               string   `json:"order_id"`
	CaseType              string   `json:"case_type"`
	ExpectedDecision      string   `json:"expected_decision"`
	ExpectedPaymentIDs    []string `json:"expected_payment_ids"`
	ExpectedUTRs          []string `json:"expected_utrs,omitempty"`
	ExpectedSettlementIDs []string `json:"expected_settlement_ids,omitempty"`
	Notes                 string   `json:"notes"`
}

const dateFmt = "2006-01-02"

var customers = []string{
	"Amit", "Priya", "Rahul", "Sneha", "Vikram", "Anjali", "Karan", "Neha",
	"Arjun", "Divya", "Rohan", "Kavya", "Sanjay", "Meera", "Aditya", "Pooja",
	"Nikhil", "Ritu", "Suresh", "Ishita",
}

// computeFees applies a flat 1.8% gateway fee plus 18% GST on that fee,
// mirroring Razorpay's published fee structure. Integer paise only.
func computeFees(gross int64) (fee, tax, net int64) {
	fee = gross * 18 / 1000
	tax = fee * 18 / 100
	net = gross - fee - tax
	return
}

func main() {
	outDir := filepath.Join("data", "fixtures")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		log.Fatal(err)
	}

	ledger, gateway, bank, ground := generate()

	writeJSON(filepath.Join(outDir, "ledger.json"), ledger)
	writeJSON(filepath.Join(outDir, "gateway.json"), gateway)
	writeJSON(filepath.Join(outDir, "bank.json"), bank)
	writeJSON(filepath.Join(outDir, "ground_truth.json"), ground)

	log.Printf("generated %d ledger, %d gateway, %d bank records (%d ground-truth entries)",
		len(ledger), len(gateway), len(bank), len(ground))
}

// generate deterministically builds the full fixture set in memory.
func generate() ([]ledgerRecord, []gatewayRecord, []bankRecord, []groundTruthEntry) {
	baseDate := mustParse("2026-08-01")

	var ledger []ledgerRecord
	var gateway []gatewayRecord
	var bank []bankRecord
	var ground []groundTruthEntry

	orderSeq := 1001
	paySeq := 1001
	settleSeq := 501
	utrSeq := 1

	nextOrderID := func() string { s := orderSeq; orderSeq++; return fmt.Sprintf("ORD_%d", s) }
	nextPaymentID := func() string { s := paySeq; paySeq++; return fmt.Sprintf("PAY_%d", s) }
	nextSettlementID := func() string { s := settleSeq; settleSeq++; return fmt.Sprintf("ST_%d", s) }
	nextUTR := func() string { s := utrSeq; utrSeq++; return fmt.Sprintf("UTR%06d", s) }
	customerFor := func(i int) string { return customers[i%len(customers)] }

	dayOffset := 0
	nextDate := func() time.Time {
		d := baseDate.AddDate(0, 0, dayOffset)
		dayOffset++
		if dayOffset%3 == 0 {
			dayOffset++ // occasional skip so dates aren't perfectly sequential
		}
		return d
	}
	grossFor := func(i int) int64 {
		// deterministic spread, roughly ₹1,500 – ₹21,500
		return 150000 + int64((i*9173)%2000000)
	}

	// --- 40 clean records: exact reference match, 0-1 day settlement lag ---
	for i := 0; i < 40; i++ {
		orderID := nextOrderID()
		paymentID := nextPaymentID()
		settlementID := nextSettlementID()
		gross := grossFor(i)
		fee, tax, net := computeFees(gross)
		ledgerDate := nextDate()
		lag := int64(i % 2) // 0 or 1 day

		ledger = append(ledger, ledgerRecord{orderID, customerFor(i), gross, "PAID"})
		gateway = append(gateway, gatewayRecord{
			PaymentID: paymentID, OrderID: orderID, GrossAmount: gross,
			Fee: fee, Tax: tax, NetAmount: net, SettlementID: settlementID,
			SettlementDate: ledgerDate.Format(dateFmt),
		})
		utr := nextUTR()
		bank = append(bank, bankRecord{
			UTR: utr, Date: ledgerDate.AddDate(0, 0, int(lag)).Format(dateFmt),
			CreditAmount: net, Description: "RAZORPAY " + paymentID,
		})
		ground = append(ground, groundTruthEntry{
			OrderID: orderID, CaseType: "NORMAL", ExpectedDecision: "MATCHED",
			ExpectedPaymentIDs: []string{paymentID}, ExpectedUTRs: []string{utr},
			ExpectedSettlementIDs: []string{settlementID},
			Notes:                 "Exact reference match, fee/tax-adjusted net, settlement lag 0-1 days.",
		})
	}

	// --- 5 date-lag records: 2-3 day gap between gateway settlement and bank credit ---
	for i := 0; i < 5; i++ {
		orderID := nextOrderID()
		paymentID := nextPaymentID()
		settlementID := nextSettlementID()
		gross := grossFor(40 + i)
		fee, tax, net := computeFees(gross)
		ledgerDate := nextDate()
		lag := int64(2 + i%2) // 2 or 3 days

		ledger = append(ledger, ledgerRecord{orderID, customerFor(40 + i), gross, "PAID"})
		gateway = append(gateway, gatewayRecord{
			PaymentID: paymentID, OrderID: orderID, GrossAmount: gross,
			Fee: fee, Tax: tax, NetAmount: net, SettlementID: settlementID,
			SettlementDate: ledgerDate.Format(dateFmt),
		})
		utr := nextUTR()
		bank = append(bank, bankRecord{
			UTR: utr, Date: ledgerDate.AddDate(0, 0, int(lag)).Format(dateFmt),
			CreditAmount: net, Description: "RAZORPAY " + paymentID,
		})
		ground = append(ground, groundTruthEntry{
			OrderID: orderID, CaseType: "DATE_LAG", ExpectedDecision: "MATCHED",
			ExpectedPaymentIDs: []string{paymentID}, ExpectedUTRs: []string{utr},
			ExpectedSettlementIDs: []string{settlementID},
			Notes:                 "Settlement lag is 2-3 days; requires the date-window tolerance, not same-day matching.",
		})
	}

	// --- batch: 3 ledger orders settled together as one bank credit (N:1) ---
	{
		settlementID := nextSettlementID()
		ledgerDate := nextDate()
		var paymentIDs []string
		var orderIDs []string
		var netSum int64
		for i := 0; i < 3; i++ {
			orderID := nextOrderID()
			paymentID := nextPaymentID()
			gross := grossFor(45 + i)
			fee, tax, net := computeFees(gross)
			ledger = append(ledger, ledgerRecord{orderID, customerFor(45 + i), gross, "PAID"})
			gateway = append(gateway, gatewayRecord{
				PaymentID: paymentID, OrderID: orderID, GrossAmount: gross,
				Fee: fee, Tax: tax, NetAmount: net, SettlementID: settlementID,
				SettlementDate: ledgerDate.Format(dateFmt),
			})
			paymentIDs = append(paymentIDs, paymentID)
			orderIDs = append(orderIDs, orderID)
			netSum += net
		}
		utr := nextUTR()
		bank = append(bank, bankRecord{
			UTR: utr, Date: ledgerDate.AddDate(0, 0, 1).Format(dateFmt),
			CreditAmount: netSum, Description: "RAZORPAY SETTLEMENT " + settlementID,
		})
		for i, orderID := range orderIDs {
			ground = append(ground, groundTruthEntry{
				OrderID: orderID, CaseType: "BATCH", ExpectedDecision: "MATCHED",
				ExpectedPaymentIDs: []string{paymentIDs[i]}, ExpectedUTRs: []string{utr},
				ExpectedSettlementIDs: []string{settlementID},
				Notes:                 "One of 3 orders batched into a single settlement; bank credit equals the sum of all 3 net amounts (N:1 sum match).",
			})
		}
	}

	// --- refund: gateway net amount reduced by a partial refund after settlement ---
	{
		orderID := nextOrderID()
		paymentID := nextPaymentID()
		settlementID := nextSettlementID()
		gross := grossFor(48)
		fee, tax, net := computeFees(gross)
		refund := gross / 10 // 10% partial refund
		netAfterRefund := net - refund
		ledgerDate := nextDate()

		ledger = append(ledger, ledgerRecord{orderID, customerFor(48), gross, "PAID"})
		gateway = append(gateway, gatewayRecord{
			PaymentID: paymentID, OrderID: orderID, GrossAmount: gross,
			Fee: fee, Tax: tax, RefundAmount: refund, NetAmount: netAfterRefund,
			SettlementID: settlementID, SettlementDate: ledgerDate.Format(dateFmt),
		})
		utr := nextUTR()
		bank = append(bank, bankRecord{
			UTR: utr, Date: ledgerDate.AddDate(0, 0, 1).Format(dateFmt),
			CreditAmount: netAfterRefund, Description: "RAZORPAY " + paymentID,
		})
		ground = append(ground, groundTruthEntry{
			OrderID: orderID, CaseType: "REFUND", ExpectedDecision: "DISCREPANCY",
			ExpectedPaymentIDs: []string{paymentID}, ExpectedUTRs: []string{utr},
			ExpectedSettlementIDs: []string{settlementID},
			Notes:                 "Bank credit is net of a partial refund; plain fee/tax formula alone will not reconcile it — refund_amount must be accounted for. Evidence is sufficient (payment_id ties the chain) so it should resolve as a DISCREPANCY, not UNRESOLVED.",
		})
	}

	// --- duplicate candidate: two orders with identical amount/date and no distinguishing reference ---
	{
		ledgerDate := nextDate()
		gross := int64(500000) // identical for both orders on purpose
		fee, tax, net := computeFees(gross)
		var orderIDs, paymentIDs, utrs []string
		for i := 0; i < 2; i++ {
			orderID := nextOrderID()
			paymentID := nextPaymentID()
			settlementID := nextSettlementID()
			ledger = append(ledger, ledgerRecord{orderID, customerFor(49 + i), gross, "PAID"})
			gateway = append(gateway, gatewayRecord{
				PaymentID: paymentID, OrderID: orderID, GrossAmount: gross,
				Fee: fee, Tax: tax, NetAmount: net, SettlementID: settlementID,
				SettlementDate: ledgerDate.Format(dateFmt),
			})
			utr := nextUTR()
			bank = append(bank, bankRecord{
				UTR: utr, Date: ledgerDate.AddDate(0, 0, 1).Format(dateFmt),
				CreditAmount: net, Description: "RAZORPAY SETTLEMENT", // generic — no payment_id, deliberately ambiguous
			})
			orderIDs = append(orderIDs, orderID)
			paymentIDs = append(paymentIDs, paymentID)
			utrs = append(utrs, utr)
		}
		for i, orderID := range orderIDs {
			ground = append(ground, groundTruthEntry{
				OrderID: orderID, CaseType: "DUPLICATE", ExpectedDecision: "UNRESOLVED",
				ExpectedPaymentIDs: []string{paymentIDs[i]}, ExpectedUTRs: utrs,
				Notes: "Two orders share identical amount, date, and net amount; bank descriptions carry no payment_id, so neither the deterministic engine nor the AI judge can safely pick which credit belongs to which order. Must stay UNRESOLVED, never guessed.",
			})
		}
	}

	// --- genuinely missing bank entry ---
	{
		orderID := nextOrderID()
		paymentID := nextPaymentID()
		settlementID := nextSettlementID()
		gross := grossFor(51)
		fee, tax, net := computeFees(gross)
		ledgerDate := nextDate()

		ledger = append(ledger, ledgerRecord{orderID, customerFor(51), gross, "PAID"})
		gateway = append(gateway, gatewayRecord{
			PaymentID: paymentID, OrderID: orderID, GrossAmount: gross,
			Fee: fee, Tax: tax, NetAmount: net, SettlementID: settlementID,
			SettlementDate: ledgerDate.Format(dateFmt),
		})
		// deliberately no bank record written
		ground = append(ground, groundTruthEntry{
			OrderID: orderID, CaseType: "MISSING_BANK", ExpectedDecision: "UNRESOLVED",
			ExpectedPaymentIDs: []string{paymentID},
			Notes:              "Ledger and gateway agree, but no bank credit exists at all. Real operational gap — must surface as MISSING_IN_BANK, not be silently dropped.",
		})
	}

	return ledger, gateway, bank, ground
}

func writeJSON(path string, v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		log.Fatal(err)
	}
}

func mustParse(s string) time.Time {
	t, err := time.Parse(dateFmt, s)
	if err != nil {
		log.Fatal(err)
	}
	return t
}
