// Package gen generates a fresh, internally-consistent synthetic
// ledger/gateway/bank/ground-truth batch on demand, parameterized by
// size and seed — the "let judges bring their own data" Tier 1 feature
// (CLAUDE.md Phase 16). It is a separate, independent implementation
// from cmd/gen's fixed 52-record generator, deliberately: cmd/gen
// produces the frozen data/fixtures/*.json files that every other
// package's tests already depend on byte-for-byte (e.g.
// match_test.go's exact 48/1/2/1 decision-count cross-check), and this
// package must never risk changing that. Some structure is duplicated
// rather than shared for that reason.
package gen

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"time"
)

// MinSize/MaxSize bound what a caller may request. A public "generate"
// endpoint is not a place to let arbitrary input make a demo click take
// forever or burn through Vertex AI quota — the dashboard only ever
// offers a small fixed choice within this range (Precision pass note:
// preset buttons, not a free-form field).
const (
	MinSize = 10
	MaxSize = 500
)

type LedgerRecord struct {
	OrderID     string `json:"order_id"`
	Customer    string `json:"customer"`
	GrossAmount int64  `json:"gross_amount"`
	Status      string `json:"status"`
}

type GatewayRecord struct {
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

type BankRecord struct {
	UTR          string `json:"utr"`
	Date         string `json:"date"`
	CreditAmount int64  `json:"credit_amount"`
	Description  string `json:"description"`
}

type GroundTruthEntry struct {
	OrderID               string   `json:"order_id"`
	CaseType              string   `json:"case_type"`
	ExpectedDecision      string   `json:"expected_decision"`
	ExpectedPaymentIDs    []string `json:"expected_payment_ids"`
	ExpectedUTRs          []string `json:"expected_utrs,omitempty"`
	ExpectedSettlementIDs []string `json:"expected_settlement_ids,omitempty"`
	Notes                 string   `json:"notes"`
}

// Batch is one generated fixture set, in memory.
type Batch struct {
	Ledger      []LedgerRecord
	Gateway     []GatewayRecord
	Bank        []BankRecord
	GroundTruth []GroundTruthEntry
}

// Sample is a small, human-checkable slice of a generated batch — a few
// records from each source. It exists so the dashboard can show a judge
// concrete proof that a new seed really produced new data (different
// customers, amounts, dates, order IDs), not the same canned scenario
// replaying — much more convincing than trusting a percentage that
// clusters in the same range by design.
type Sample struct {
	Ledger  []LedgerRecord  `json:"ledger"`
	Gateway []GatewayRecord `json:"gateway"`
	Bank    []BankRecord    `json:"bank"`
}

// Preview returns the first n records of each source (n bounded by
// what's available). The batch leads with ordinary orders, and every
// scenario appends its ledger/gateway rows in the same order, so
// row i of each slice refers to the same order — the preview reads as
// one coherent INTERNAL ORDER → GATEWAY → BANK chain, not three
// unrelated rows.
func (b Batch) Preview(n int) Sample {
	return Sample{
		Ledger:  b.Ledger[:min(n, len(b.Ledger))],
		Gateway: b.Gateway[:min(n, len(b.Gateway))],
		Bank:    b.Bank[:min(n, len(b.Bank))],
	}
}

const dateFmt = "2006-01-02"

var customers = []string{
	"Amit", "Priya", "Rahul", "Sneha", "Vikram", "Anjali", "Karan", "Neha",
	"Arjun", "Divya", "Rohan", "Kavya", "Sanjay", "Meera", "Aditya", "Pooja",
	"Nikhil", "Ritu", "Suresh", "Ishita",
}

// computeFees mirrors cmd/gen's fee model: a flat 1.8% gateway fee plus
// 18% GST on that fee. Integer paise only.
func computeFees(gross int64) (fee, tax, net int64) {
	fee = gross * 18 / 1000
	tax = fee * 18 / 100
	net = gross - fee - tax
	return
}

// scenario is one case type the generator can emit, its draw weight,
// and how many ledger orders it produces. NORMAL dominates; the rest
// are the deliberate anomalies.
type scenario struct {
	name   string
	weight float64
	cost   int
	emit   func(*builder)
}

// scenarioTable defines *controlled randomness*: each order (or group)
// is drawn from this weighted distribution using the seeded RNG, rather
// than the batch being built to fixed per-category counts. So the
// realized mix — and therefore every headline metric on the dashboard —
// varies genuinely from seed to seed (binomial variance), while the
// expected non-normal share stays inside Phase 0's ~15-30% target and
// the run stays fully reproducible for a fixed seed (Implementation
// Rule 7).
//
// CROSS_MATCH is the deliberately engine-fallible case: the
// deterministic engine gets it wrong by construction, so ground-truth
// accuracy is a genuinely measured number below 100%, not 100% because
// every generated scenario was one the engine was built to ace.
var scenarioTable = []scenario{
	{"NORMAL", 0.865, 1, (*builder).addNormal},
	{"DATE_LAG", 0.06, 1, (*builder).addDateLag},
	{"BATCH", 0.018, 3, (*builder).addBatch},
	{"REFUND", 0.018, 1, (*builder).addRefund},
	{"DUPLICATE", 0.014, 2, (*builder).addDuplicatePair},
	{"MISSING_BANK", 0.012, 1, (*builder).addMissingBank},
	{"CROSS_MATCH", 0.013, 2, (*builder).addCrossMatch},
}

// Generate builds a batch of exactly `size` orders for a given seed by
// drawing each order/group from scenarioTable. Same (size, seed) always
// produces byte-identical output; a different seed produces a genuinely
// different batch — different customers, amounts, dates, AND a
// different anomaly mix, so the dashboard's four headline numbers move
// run to run instead of being pinned by construction.
func Generate(size int, seed int64) Batch {
	if size < MinSize {
		size = MinSize
	}
	if size > MaxSize {
		size = MaxSize
	}
	rng := rand.New(rand.NewSource(seed))
	b := &builder{rng: rng, baseDate: mustParse("2026-08-01")}

	// Two scenarios are load-bearing for the demo and must appear in
	// every batch however the dice fall: one DUPLICATE (the AMBIGUOUS →
	// AI-judge path) and one CROSS_MATCH (the engine-fallible case that
	// makes ground-truth accuracy a real measurement, not 100% by
	// construction). Reserve their 4 order slots, fill the rest by
	// weighted draw, then append them last so the batch still leads with
	// ordinary orders. Everything else — including whether a given batch
	// has any REFUND or BATCH case at all — is left to the draw.
	fillTarget := size - 4
	for b.orderSeq < fillTarget {
		s := drawScenario(rng)
		if b.orderSeq+s.cost > fillTarget {
			s = scenarioTable[0] // NORMAL — never overshoot
		}
		s.emit(b)
	}
	b.addDuplicatePair()
	b.addCrossMatch()

	return Batch{Ledger: b.ledger, Gateway: b.gateway, Bank: b.bank, GroundTruth: b.ground}
}

// drawScenario picks one scenario from scenarioTable weighted by
// scenario.weight.
func drawScenario(rng *rand.Rand) scenario {
	var total float64
	for _, s := range scenarioTable {
		total += s.weight
	}
	x := rng.Float64() * total
	for _, s := range scenarioTable {
		if x < s.weight {
			return s
		}
		x -= s.weight
	}
	return scenarioTable[0]
}

type builder struct {
	rng       *rand.Rand
	baseDate  time.Time
	dayOffset int

	orderSeq, paySeq, settleSeq, utrSeq int

	ledger  []LedgerRecord
	gateway []GatewayRecord
	bank    []BankRecord
	ground  []GroundTruthEntry
}

func (b *builder) nextOrderID() string {
	b.orderSeq++
	return fmt.Sprintf("ORD_%d", 1000+b.orderSeq)
}
func (b *builder) nextPaymentID() string {
	b.paySeq++
	return fmt.Sprintf("PAY_%d", 1000+b.paySeq)
}
func (b *builder) nextSettlementID() string {
	b.settleSeq++
	return fmt.Sprintf("ST_%d", 500+b.settleSeq)
}
func (b *builder) nextUTR() string {
	b.utrSeq++
	return fmt.Sprintf("UTR%06d", b.utrSeq)
}
func (b *builder) randomCustomer() string {
	return customers[b.rng.Intn(len(customers))]
}

// randomGross returns a deterministic-per-seed gross amount in roughly
// ₹1,500-₹21,500 (paise), matching cmd/gen's original scale.
func (b *builder) randomGross() int64 {
	return 150000 + b.rng.Int63n(2000000)
}

func (b *builder) nextDate() time.Time {
	d := b.baseDate.AddDate(0, 0, b.dayOffset)
	b.dayOffset++
	if b.rng.Intn(3) == 0 {
		b.dayOffset++ // occasional skip so dates aren't perfectly sequential
	}
	return d
}

func (b *builder) addNormal() {
	orderID, paymentID, settlementID := b.nextOrderID(), b.nextPaymentID(), b.nextSettlementID()
	gross := b.randomGross()
	fee, tax, net := computeFees(gross)
	ledgerDate := b.nextDate()
	lag := int64(b.rng.Intn(2)) // 0 or 1 day

	b.ledger = append(b.ledger, LedgerRecord{orderID, b.randomCustomer(), gross, "PAID"})
	b.gateway = append(b.gateway, GatewayRecord{
		PaymentID: paymentID, OrderID: orderID, GrossAmount: gross,
		Fee: fee, Tax: tax, NetAmount: net, SettlementID: settlementID,
		SettlementDate: ledgerDate.Format(dateFmt),
	})
	utr := b.nextUTR()
	b.bank = append(b.bank, BankRecord{
		UTR: utr, Date: ledgerDate.AddDate(0, 0, int(lag)).Format(dateFmt),
		CreditAmount: net, Description: "RAZORPAY " + paymentID,
	})
	b.ground = append(b.ground, GroundTruthEntry{
		OrderID: orderID, CaseType: "NORMAL", ExpectedDecision: "MATCHED",
		ExpectedPaymentIDs: []string{paymentID}, ExpectedUTRs: []string{utr},
		ExpectedSettlementIDs: []string{settlementID},
		Notes:                 "Exact reference match, fee/tax-adjusted net, settlement lag 0-1 days.",
	})
}

func (b *builder) addDateLag() {
	orderID, paymentID, settlementID := b.nextOrderID(), b.nextPaymentID(), b.nextSettlementID()
	gross := b.randomGross()
	fee, tax, net := computeFees(gross)
	ledgerDate := b.nextDate()
	lag := int64(2 + b.rng.Intn(2)) // 2 or 3 days

	b.ledger = append(b.ledger, LedgerRecord{orderID, b.randomCustomer(), gross, "PAID"})
	b.gateway = append(b.gateway, GatewayRecord{
		PaymentID: paymentID, OrderID: orderID, GrossAmount: gross,
		Fee: fee, Tax: tax, NetAmount: net, SettlementID: settlementID,
		SettlementDate: ledgerDate.Format(dateFmt),
	})
	utr := b.nextUTR()
	b.bank = append(b.bank, BankRecord{
		UTR: utr, Date: ledgerDate.AddDate(0, 0, int(lag)).Format(dateFmt),
		CreditAmount: net, Description: "RAZORPAY " + paymentID,
	})
	b.ground = append(b.ground, GroundTruthEntry{
		OrderID: orderID, CaseType: "DATE_LAG", ExpectedDecision: "MATCHED",
		ExpectedPaymentIDs: []string{paymentID}, ExpectedUTRs: []string{utr},
		ExpectedSettlementIDs: []string{settlementID},
		Notes:                 "Settlement lag is 2-3 days; requires the date-window tolerance, not same-day matching.",
	})
}

func (b *builder) addBatch() {
	settlementID := b.nextSettlementID()
	ledgerDate := b.nextDate()
	var paymentIDs, orderIDs []string
	var netSum int64
	for i := 0; i < 3; i++ {
		orderID, paymentID := b.nextOrderID(), b.nextPaymentID()
		gross := b.randomGross()
		fee, tax, net := computeFees(gross)
		b.ledger = append(b.ledger, LedgerRecord{orderID, b.randomCustomer(), gross, "PAID"})
		b.gateway = append(b.gateway, GatewayRecord{
			PaymentID: paymentID, OrderID: orderID, GrossAmount: gross,
			Fee: fee, Tax: tax, NetAmount: net, SettlementID: settlementID,
			SettlementDate: ledgerDate.Format(dateFmt),
		})
		paymentIDs = append(paymentIDs, paymentID)
		orderIDs = append(orderIDs, orderID)
		netSum += net
	}
	utr := b.nextUTR()
	b.bank = append(b.bank, BankRecord{
		UTR: utr, Date: ledgerDate.AddDate(0, 0, 1).Format(dateFmt),
		CreditAmount: netSum, Description: "RAZORPAY SETTLEMENT " + settlementID,
	})
	for i, orderID := range orderIDs {
		b.ground = append(b.ground, GroundTruthEntry{
			OrderID: orderID, CaseType: "BATCH", ExpectedDecision: "MATCHED",
			ExpectedPaymentIDs: []string{paymentIDs[i]}, ExpectedUTRs: []string{utr},
			ExpectedSettlementIDs: []string{settlementID},
			Notes:                 "One of 3 orders batched into a single settlement; bank credit equals the sum of all 3 net amounts (N:1 sum match).",
		})
	}
}

func (b *builder) addRefund() {
	orderID, paymentID, settlementID := b.nextOrderID(), b.nextPaymentID(), b.nextSettlementID()
	gross := b.randomGross()
	fee, tax, net := computeFees(gross)
	refund := gross / 10 // 10% partial refund
	netAfterRefund := net - refund
	ledgerDate := b.nextDate()

	b.ledger = append(b.ledger, LedgerRecord{orderID, b.randomCustomer(), gross, "PAID"})
	b.gateway = append(b.gateway, GatewayRecord{
		PaymentID: paymentID, OrderID: orderID, GrossAmount: gross,
		Fee: fee, Tax: tax, RefundAmount: refund, NetAmount: netAfterRefund,
		SettlementID: settlementID, SettlementDate: ledgerDate.Format(dateFmt),
	})
	utr := b.nextUTR()
	b.bank = append(b.bank, BankRecord{
		UTR: utr, Date: ledgerDate.AddDate(0, 0, 1).Format(dateFmt),
		CreditAmount: netAfterRefund, Description: "RAZORPAY " + paymentID,
	})
	b.ground = append(b.ground, GroundTruthEntry{
		OrderID: orderID, CaseType: "REFUND", ExpectedDecision: "DISCREPANCY",
		ExpectedPaymentIDs: []string{paymentID}, ExpectedUTRs: []string{utr},
		ExpectedSettlementIDs: []string{settlementID},
		Notes:                 "Bank credit is net of a partial refund; plain fee/tax formula alone will not reconcile it. Evidence is sufficient (payment_id ties the chain) so it resolves as a DISCREPANCY, not UNRESOLVED.",
	})
}

func (b *builder) addDuplicatePair() {
	ledgerDate := b.nextDate()
	gross := int64(500000) // identical for both orders on purpose
	fee, tax, net := computeFees(gross)
	var orderIDs, paymentIDs, utrs []string
	for i := 0; i < 2; i++ {
		orderID, paymentID, settlementID := b.nextOrderID(), b.nextPaymentID(), b.nextSettlementID()
		b.ledger = append(b.ledger, LedgerRecord{orderID, b.randomCustomer(), gross, "PAID"})
		b.gateway = append(b.gateway, GatewayRecord{
			PaymentID: paymentID, OrderID: orderID, GrossAmount: gross,
			Fee: fee, Tax: tax, NetAmount: net, SettlementID: settlementID,
			SettlementDate: ledgerDate.Format(dateFmt),
		})
		utr := b.nextUTR()
		b.bank = append(b.bank, BankRecord{
			UTR: utr, Date: ledgerDate.AddDate(0, 0, 1).Format(dateFmt),
			CreditAmount: net, Description: "RAZORPAY SETTLEMENT", // generic — deliberately ambiguous
		})
		orderIDs = append(orderIDs, orderID)
		paymentIDs = append(paymentIDs, paymentID)
		utrs = append(utrs, utr)
	}
	for i, orderID := range orderIDs {
		b.ground = append(b.ground, GroundTruthEntry{
			OrderID: orderID, CaseType: "DUPLICATE", ExpectedDecision: "UNRESOLVED",
			ExpectedPaymentIDs: []string{paymentIDs[i]}, ExpectedUTRs: utrs,
			Notes: "Two orders share identical amount, date, and net amount; bank descriptions carry no payment_id. Must stay UNRESOLVED, never guessed.",
		})
	}
}

func (b *builder) addMissingBank() {
	orderID, paymentID, settlementID := b.nextOrderID(), b.nextPaymentID(), b.nextSettlementID()
	gross := b.randomGross()
	fee, tax, net := computeFees(gross)
	ledgerDate := b.nextDate()

	b.ledger = append(b.ledger, LedgerRecord{orderID, b.randomCustomer(), gross, "PAID"})
	b.gateway = append(b.gateway, GatewayRecord{
		PaymentID: paymentID, OrderID: orderID, GrossAmount: gross,
		Fee: fee, Tax: tax, NetAmount: net, SettlementID: settlementID,
		SettlementDate: ledgerDate.Format(dateFmt),
	})
	// deliberately no bank record written
	b.ground = append(b.ground, GroundTruthEntry{
		OrderID: orderID, CaseType: "MISSING_BANK", ExpectedDecision: "UNRESOLVED",
		ExpectedPaymentIDs: []string{paymentID},
		Notes:              "Ledger and gateway agree, but no bank credit exists at all.",
	})
}

// addCrossMatch emits the batch's deliberately engine-fallible case:
// two orders with an identical net amount and nearby settlement dates,
// generic (reference-free) bank descriptions, and exactly ONE bank
// credit between them — which genuinely belongs to the second order.
// The first order's settlement never reached the bank.
//
// The deterministic engine matches on amount + date alone when there's
// no payment reference, and processes orders in ledger order, so it
// wrongly consumes the second order's credit for the first: a false
// MATCHED on the order that was never paid (and so NOT even flagged as
// an exception), and a false UNRESOLVED on the order that actually was.
// ground_truth.json records the truth, so metrics.Compute scores these
// two as misses — which is the whole point: the accuracy number is
// measured against cases the engine can fail, not just cases it aces.
func (b *builder) addCrossMatch() {
	date := b.nextDate()
	gross := int64(700000) // fixed + distinct from the DUPLICATE case's 500000, so no third order collides on amount
	fee, tax, net := computeFees(gross)

	victimID, victimPay, victimSettle := b.nextOrderID(), b.nextPaymentID(), b.nextSettlementID()
	paidID, paidPay, paidSettle := b.nextOrderID(), b.nextPaymentID(), b.nextSettlementID()

	b.ledger = append(b.ledger,
		LedgerRecord{victimID, b.randomCustomer(), gross, "PAID"},
		LedgerRecord{paidID, b.randomCustomer(), gross, "PAID"},
	)
	b.gateway = append(b.gateway,
		GatewayRecord{
			PaymentID: victimPay, OrderID: victimID, GrossAmount: gross,
			Fee: fee, Tax: tax, NetAmount: net, SettlementID: victimSettle,
			SettlementDate: date.Format(dateFmt),
		},
		GatewayRecord{
			PaymentID: paidPay, OrderID: paidID, GrossAmount: gross,
			Fee: fee, Tax: tax, NetAmount: net, SettlementID: paidSettle,
			SettlementDate: date.Format(dateFmt),
		},
	)

	// one credit only, generic description, genuinely the paid order's
	utr := b.nextUTR()
	b.bank = append(b.bank, BankRecord{
		UTR: utr, Date: date.AddDate(0, 0, 1).Format(dateFmt),
		CreditAmount: net, Description: "RAZORPAY SETTLEMENT",
	})

	b.ground = append(b.ground,
		GroundTruthEntry{
			OrderID: victimID, CaseType: "CROSS_MATCH_VICTIM", ExpectedDecision: "UNRESOLVED",
			ExpectedPaymentIDs: []string{victimPay},
			Notes:              "This order's settlement never reached the bank. Another order in the batch has an identical net amount and a nearby date, and no bank description carries a payment reference, so the amount-only matcher wrongly consumes that order's credit for this one. The engine reports MATCHED and raises no exception — a silent false positive that only ground-truth scoring catches.",
		},
		GroundTruthEntry{
			OrderID: paidID, CaseType: "CROSS_MATCH_TRUE", ExpectedDecision: "MATCHED",
			ExpectedPaymentIDs: []string{paidPay}, ExpectedUTRs: []string{utr},
			ExpectedSettlementIDs: []string{paidSettle},
			Notes:                 "This order genuinely was settled, but the engine assigns its bank credit to the earlier same-amount order and leaves this one UNRESOLVED.",
		},
	)
}

// WriteTo writes the batch's four JSON files into dir (created if
// needed) using exactly the field names internal/ingest already
// expects — the same shape cmd/gen's frozen fixtures use, so the
// generated batch flows through the existing, unchanged ingest
// parsers and validators with no special-casing.
func (b Batch) WriteTo(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("gen: create output dir: %w", err)
	}
	for name, v := range map[string]any{
		"ledger.json":       b.Ledger,
		"gateway.json":      b.Gateway,
		"bank.json":         b.Bank,
		"ground_truth.json": b.GroundTruth,
	} {
		data, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Errorf("gen: marshal %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return fmt.Errorf("gen: write %s: %w", name, err)
		}
	}
	return nil
}

func mustParse(s string) time.Time {
	t, err := time.Parse(dateFmt, s)
	if err != nil {
		panic(err) // dateFmt/s are both compile-time constants here
	}
	return t
}
