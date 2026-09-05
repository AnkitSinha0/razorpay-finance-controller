package ingest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"razorpay_finance_controller/internal/models"
)

type bankRecord struct {
	UTR          string `json:"utr"`
	Date         string `json:"date"`
	CreditAmount int64  `json:"credit_amount"`
	Description  string `json:"description"`
}

// LoadBank reads and validates the bank statement fixture at path,
// returning one models.Transaction per record.
func LoadBank(path string) ([]models.Transaction, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ingest: read bank file: %w", err)
	}

	var records []bankRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, fmt.Errorf("ingest: parse bank JSON: %w", err)
	}

	txns := make([]models.Transaction, 0, len(records))
	var errs []error
	for i, r := range records {
		date, err := validateBankRecord(r)
		if err != nil {
			errs = append(errs, fmt.Errorf("bank[%d] (utr=%q): %w", i, r.UTR, err))
			continue
		}
		txns = append(txns, models.Transaction{
			ID:           "BANK:" + r.UTR,
			Source:       models.SourceBank,
			UTR:          r.UTR,
			CreditAmount: r.CreditAmount,
			Date:         date,
			Description:  r.Description,
		})
	}

	if len(errs) > 0 {
		return txns, fmt.Errorf("ingest: %d invalid bank record(s): %w", len(errs), errors.Join(errs...))
	}
	return txns, nil
}

func validateBankRecord(r bankRecord) (time.Time, error) {
	if r.UTR == "" {
		return time.Time{}, errors.New("utr is required")
	}
	if r.CreditAmount <= 0 {
		return time.Time{}, fmt.Errorf("credit_amount must be positive, got %d", r.CreditAmount)
	}
	if r.Description == "" {
		return time.Time{}, errors.New("description is required")
	}
	if r.Date == "" {
		return time.Time{}, errors.New("date is required")
	}
	date, err := time.Parse(dateLayout, r.Date)
	if err != nil {
		return time.Time{}, fmt.Errorf("date %q is invalid: %w", r.Date, err)
	}
	return date, nil
}
