package ingest

import (
	"path/filepath"
	"testing"
)

// TestLoadAll_RealFixtures normalizes the actual data/fixtures files
// produced by cmd/gen (Phase 0) end to end, so a Phase 0 change that
// breaks ingest validation is caught immediately.
func TestLoadAll_RealFixtures(t *testing.T) {
	dir := filepath.Join("..", "..", "data", "fixtures")

	batch, err := LoadAll(dir)
	if err != nil {
		t.Fatalf("LoadAll failed on real fixtures: %v", err)
	}

	if len(batch.Ledger) < 50 {
		t.Errorf("ledger count = %d, want >= 50", len(batch.Ledger))
	}
	if len(batch.Gateway) != len(batch.Ledger) {
		t.Errorf("gateway count = %d, want %d", len(batch.Gateway), len(batch.Ledger))
	}
	if len(batch.Bank) == 0 {
		t.Error("bank count = 0, want > 0")
	}

	for _, txn := range batch.Ledger {
		if txn.Source != "LEDGER" {
			t.Errorf("ledger transaction has Source %q", txn.Source)
		}
	}
	for _, txn := range batch.Gateway {
		if txn.Source != "GATEWAY" {
			t.Errorf("gateway transaction has Source %q", txn.Source)
		}
		if txn.Date.IsZero() {
			t.Errorf("gateway transaction %s has zero Date", txn.PaymentID)
		}
	}
	for _, txn := range batch.Bank {
		if txn.Source != "BANK" {
			t.Errorf("bank transaction has Source %q", txn.Source)
		}
		if txn.Date.IsZero() {
			t.Errorf("bank transaction %s has zero Date", txn.UTR)
		}
	}
}
