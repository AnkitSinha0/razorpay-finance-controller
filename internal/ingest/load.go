package ingest

import (
	"fmt"
	"path/filepath"

	"razorpay_finance_controller/internal/models"
)

// Batch holds the three normalized transaction sets produced by a
// reconciliation run's ingest step.
type Batch struct {
	Ledger  []models.Transaction
	Gateway []models.Transaction
	Bank    []models.Transaction
}

// LoadAll reads ledger.json, gateway.json, and bank.json from dir and
// normalizes them into a Batch. It fails fast on the first source that
// has any invalid record.
func LoadAll(dir string) (Batch, error) {
	ledger, err := LoadLedger(filepath.Join(dir, "ledger.json"))
	if err != nil {
		return Batch{}, fmt.Errorf("ingest: ledger: %w", err)
	}
	gateway, err := LoadGateway(filepath.Join(dir, "gateway.json"))
	if err != nil {
		return Batch{}, fmt.Errorf("ingest: gateway: %w", err)
	}
	bank, err := LoadBank(filepath.Join(dir, "bank.json"))
	if err != nil {
		return Batch{}, fmt.Errorf("ingest: bank: %w", err)
	}
	return Batch{Ledger: ledger, Gateway: gateway, Bank: bank}, nil
}
