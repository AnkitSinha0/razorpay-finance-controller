package gen

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"razorpay_finance_controller/internal/ingest"
	"razorpay_finance_controller/internal/match"
	"razorpay_finance_controller/internal/metrics"
)

// countByCaseType tallies ground-truth case types in a batch.
func countByCaseType(b Batch) map[string]int {
	m := make(map[string]int)
	for _, g := range b.GroundTruth {
		m[g.CaseType]++
	}
	return m
}

// nonNormalRatio is the share of orders that are deliberate anomalies.
func nonNormalRatio(b Batch) float64 {
	nonNormal := 0
	for _, g := range b.GroundTruth {
		if g.CaseType != "NORMAL" {
			nonNormal++
		}
	}
	return float64(nonNormal) / float64(len(b.Ledger))
}

func TestGenerate_ExactSizeAndOnePerOrder(t *testing.T) {
	for _, size := range []int{50, 100, 250} {
		b := Generate(size, 42)
		if len(b.Ledger) != size {
			t.Errorf("size %d: len(Ledger) = %d, want %d", size, len(b.Ledger), size)
		}
		if len(b.Gateway) != size {
			t.Errorf("size %d: len(Gateway) = %d, want %d", size, len(b.Gateway), size)
		}
		if len(b.GroundTruth) != size {
			t.Errorf("size %d: len(GroundTruth) = %d, want %d", size, len(b.GroundTruth), size)
		}
	}
}

// TestGenerate_SameSeedIsReproducible is Implementation Rule 7 applied
// to the generator itself: the same (size, seed) must always produce
// byte-identical output.
func TestGenerate_SameSeedIsReproducible(t *testing.T) {
	a := Generate(100, 7)
	b := Generate(100, 7)
	if len(a.Ledger) != len(b.Ledger) {
		t.Fatalf("len(Ledger) differs: %d vs %d", len(a.Ledger), len(b.Ledger))
	}
	for i := range a.Ledger {
		if a.Ledger[i] != b.Ledger[i] {
			t.Fatalf("ledger[%d] differs: %+v vs %+v", i, a.Ledger[i], b.Ledger[i])
		}
	}
	for i := range a.Gateway {
		if a.Gateway[i] != b.Gateway[i] {
			t.Fatalf("gateway[%d] differs: %+v vs %+v", i, a.Gateway[i], b.Gateway[i])
		}
	}
}

// TestGenerate_DifferentSeedsProduceDifferentButConsistentBatches is
// Phase 16 Tier 1's own test spec: two different seeds produce
// different data, but each batch is still internally consistent (every
// order's gross_amount ties out through the same fee formula the
// deterministic engine expects).
func TestGenerate_DifferentSeedsProduceDifferentButConsistentBatches(t *testing.T) {
	a := Generate(100, 1)
	b := Generate(100, 2)

	same := true
	for i := range a.Ledger {
		if a.Ledger[i].GrossAmount != b.Ledger[i].GrossAmount || a.Ledger[i].Customer != b.Ledger[i].Customer {
			same = false
			break
		}
	}
	if same {
		t.Error("two different seeds produced identical ledgers — seed isn't actually varying the output")
	}

	for _, batch := range []Batch{a, b} {
		for _, g := range batch.Gateway {
			wantFee := g.GrossAmount * 18 / 1000
			wantTax := wantFee * 18 / 100
			wantNet := g.GrossAmount - wantFee - wantTax - g.RefundAmount
			if g.NetAmount != wantNet {
				t.Errorf("%s: NetAmount = %d, want %d (gross=%d fee=%d tax=%d refund=%d)",
					g.OrderID, g.NetAmount, wantNet, g.GrossAmount, g.Fee, g.Tax, g.RefundAmount)
			}
		}
	}
}

// TestPreview_SpansAllSourcesAndIsBounded checks the sample the
// dashboard shows after a generate: a few records from each source,
// never more than asked for.
func TestPreview_SpansAllSourcesAndIsBounded(t *testing.T) {
	p := Generate(50, 5).Preview(3)
	if len(p.Ledger) != 3 || len(p.Gateway) != 3 || len(p.Bank) != 3 {
		t.Fatalf("Preview(3) = ledger %d / gateway %d / bank %d, want 3 each",
			len(p.Ledger), len(p.Gateway), len(p.Bank))
	}
	// the first n orders are NORMAL cases sharing IDs across sources
	if p.Ledger[0].OrderID != p.Gateway[0].OrderID {
		t.Errorf("preview rows don't line up: ledger %s vs gateway %s", p.Ledger[0].OrderID, p.Gateway[0].OrderID)
	}

	small := Generate(10, 5).Preview(100)
	if len(small.Bank) > len(Generate(10, 5).Bank) {
		t.Error("Preview(100) returned more bank rows than the batch has")
	}
}

func TestGenerate_SizeBoundsClamp(t *testing.T) {
	tooSmall := Generate(1, 0)
	if len(tooSmall.Ledger) < MinSize-5 { // categories may round the actual floor slightly
		t.Errorf("size 1 clamped to too few records: %d", len(tooSmall.Ledger))
	}
	tooBig := Generate(100000, 0)
	if len(tooBig.Ledger) > MaxSize {
		t.Errorf("size 100000 not clamped: got %d records, want <= %d", len(tooBig.Ledger), MaxSize)
	}
}

// TestGenerate_FlowsThroughExistingIngestAndMatch is the real proof
// this is a legitimate fixture, not just JSON-shaped data: routing a
// generated batch through the same unmodified internal/ingest parsers
// and internal/match engine every fixture-based run already uses,
// confirming zero validation errors and a sane, mostly-MATCHED outcome.
func TestGenerate_FlowsThroughExistingIngestAndMatch(t *testing.T) {
	dir := t.TempDir()
	b := Generate(100, 123)
	if err := b.WriteTo(dir); err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}
	for _, f := range []string{"ledger.json", "gateway.json", "bank.json", "ground_truth.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("expected %s to exist: %v", f, err)
		}
	}

	batch, err := ingest.LoadAll(dir)
	if err != nil {
		t.Fatalf("ingest.LoadAll failed on a generated batch: %v", err)
	}

	results := match.Run(batch)
	if len(results) != 100 {
		t.Fatalf("len(results) = %d, want 100", len(results))
	}
	var matched int
	for _, r := range results {
		if r.Decision == match.Matched {
			matched++
		}
	}
	if matched < 60 { // most of the batch should be clean NORMAL/DATE_LAG/BATCH records
		t.Errorf("matched = %d of 100, want a clear majority (generator's own MATCHED categories)", matched)
	}
}

// TestGenerate_MixVariesBySeed is the point of the weighted-draw
// rewrite: two seeds at the same size must differ in the *anomaly mix*,
// not just in customer names and amounts — otherwise the dashboard's
// headline percentages are pinned by construction and don't actually
// demonstrate the engine processing varying data.
func TestGenerate_MixVariesBySeed(t *testing.T) {
	a := countByCaseType(Generate(100, 1))
	b := countByCaseType(Generate(100, 2))
	if fmt.Sprint(a) == fmt.Sprint(b) {
		t.Errorf("two seeds produced an identical case-type mix %v — the draw isn't varying the composition", a)
	}
}

// TestGenerate_NonNormalRatioStaysBounded: controlled randomness, not
// free randomness — every batch stays inside roughly Phase 0's
// 15-30% deliberate-anomaly target regardless of seed or size.
func TestGenerate_NonNormalRatioStaysBounded(t *testing.T) {
	for _, size := range []int{50, 100, 250} {
		for _, seed := range []int64{1, 2, 3, 42, 99, 12345, 777, 2024} {
			r := nonNormalRatio(Generate(size, seed))
			if r < 0.12 || r > 0.38 {
				t.Errorf("size %d seed %d: non-normal ratio %.2f outside the controlled band [0.12, 0.38]", size, seed, r)
			}
		}
	}
}

// TestGenerate_AlwaysHasLoadBearingScenarios: however the dice fall,
// every batch has at least one DUPLICATE (the AMBIGUOUS → AI-judge
// path) and one CROSS_MATCH pair (the engine-fallible case). The demo
// depends on both always being present.
func TestGenerate_AlwaysHasLoadBearingScenarios(t *testing.T) {
	for _, size := range []int{50, 100, 250} {
		for _, seed := range []int64{1, 7, 42, 99, 12345, 55555} {
			c := countByCaseType(Generate(size, seed))
			if c["DUPLICATE"] < 2 {
				t.Errorf("size %d seed %d: no DUPLICATE pair (%v)", size, seed, c)
			}
			if c["CROSS_MATCH_VICTIM"] < 1 || c["CROSS_MATCH_TRUE"] < 1 {
				t.Errorf("size %d seed %d: missing a CROSS_MATCH pair (%v)", size, seed, c)
			}
		}
	}
}

// TestGenerate_GroundTruthAccuracyIsGenuinelyMeasured is #2 of the
// realism pass: because the batch contains cases the deterministic
// engine gets *wrong* by construction (CROSS_MATCH), ground-truth
// accuracy is a real measurement below 100%, not 100% because every
// generated scenario was one the engine was built to ace. The victim
// order is the dangerous kind of error — a false MATCHED that raises no
// exception; only ground-truth scoring catches it.
func TestGenerate_GroundTruthAccuracyIsGenuinelyMeasured(t *testing.T) {
	dir := t.TempDir()
	b := Generate(100, 2024)
	if err := b.WriteTo(dir); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	batch, err := ingest.LoadAll(dir)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	results := match.Run(batch)
	decisionByOrder := make(map[string]match.Decision, len(results))
	for _, r := range results {
		decisionByOrder[r.OrderID] = r.Decision
	}

	gt, err := metrics.LoadGroundTruth(filepath.Join(dir, "ground_truth.json"))
	if err != nil {
		t.Fatalf("LoadGroundTruth: %v", err)
	}
	s := metrics.Compute(results, nil, gt)
	if s.GroundTruthAccuracy == nil {
		t.Fatal("GroundTruthAccuracy is nil, want a measured value")
	}
	if *s.GroundTruthAccuracy >= 100 {
		t.Errorf("GroundTruthAccuracy = %.1f%%, want < 100%% (the batch contains engine-fallible cases)", *s.GroundTruthAccuracy)
	}

	// verify the specific failure mode: victim reported MATCHED by the
	// engine (a silent false positive), its true partner left UNRESOLVED.
	var checkedVictim, checkedTrue bool
	for _, g := range b.GroundTruth {
		switch g.CaseType {
		case "CROSS_MATCH_VICTIM":
			checkedVictim = true
			if decisionByOrder[g.OrderID] != match.Matched {
				t.Errorf("%s: engine decision = %s, want MATCHED (the false positive this case is built to produce)", g.OrderID, decisionByOrder[g.OrderID])
			}
			if g.ExpectedDecision != "UNRESOLVED" {
				t.Errorf("%s: ground truth = %s, want UNRESOLVED", g.OrderID, g.ExpectedDecision)
			}
		case "CROSS_MATCH_TRUE":
			checkedTrue = true
			if decisionByOrder[g.OrderID] != match.Unresolved {
				t.Errorf("%s: engine decision = %s, want UNRESOLVED (its credit was consumed by the victim order)", g.OrderID, decisionByOrder[g.OrderID])
			}
		}
	}
	if !checkedVictim || !checkedTrue {
		t.Fatal("batch had no CROSS_MATCH pair to check")
	}
}
