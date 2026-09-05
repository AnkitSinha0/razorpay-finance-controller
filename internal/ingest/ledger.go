package ingest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"razorpay_finance_controller/internal/models"
)

type ledgerRecord struct {
	OrderID     string `json:"order_id"`
	Customer    string `json:"customer"`
	GrossAmount int64  `json:"gross_amount"`
	Status      string `json:"status"`
}

// LoadLedger reads and validates the ledger fixture at path, returning
// one models.Transaction per record. All records are validated; any
// invalid records are reported together in a single joined error.
func LoadLedger(path string) ([]models.Transaction, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ingest: read ledger file: %w", err)
	}

	var records []ledgerRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, fmt.Errorf("ingest: parse ledger JSON: %w", err)
	}

	txns := make([]models.Transaction, 0, len(records))
	var errs []error
	for i, r := range records {
		if err := validateLedgerRecord(r); err != nil {
			errs = append(errs, fmt.Errorf("ledger[%d] (order_id=%q): %w", i, r.OrderID, err))
			continue
		}
		txns = append(txns, models.Transaction{
			ID:          "LEDGER:" + r.OrderID,
			Source:      models.SourceLedger,
			OrderID:     r.OrderID,
			Customer:    r.Customer,
			GrossAmount: r.GrossAmount,
			Status:      r.Status,
		})
	}

	if len(errs) > 0 {
		return txns, fmt.Errorf("ingest: %d invalid ledger record(s): %w", len(errs), errors.Join(errs...))
	}
	return txns, nil
}

func validateLedgerRecord(r ledgerRecord) error {
	if r.OrderID == "" {
		return errors.New("order_id is required")
	}
	if r.Customer == "" {
		return errors.New("customer is required")
	}
	if r.GrossAmount <= 0 {
		return fmt.Errorf("gross_amount must be positive, got %d", r.GrossAmount)
	}
	if r.Status == "" {
		return errors.New("status is required")
	}
	return nil
}
