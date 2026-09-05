package ingest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"razorpay_finance_controller/internal/models"
)

type gatewayRecord struct {
	PaymentID      string `json:"payment_id"`
	OrderID        string `json:"order_id"`
	GrossAmount    int64  `json:"gross_amount"`
	Fee            int64  `json:"fee"`
	Tax            int64  `json:"tax"`
	RefundAmount   int64  `json:"refund_amount"`
	NetAmount      int64  `json:"net_amount"`
	SettlementID   string `json:"settlement_id"`
	SettlementDate string `json:"settlement_date"`
}

// LoadGateway reads and validates the gateway/settlement fixture at
// path, returning one models.Transaction per record.
func LoadGateway(path string) ([]models.Transaction, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ingest: read gateway file: %w", err)
	}

	var records []gatewayRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, fmt.Errorf("ingest: parse gateway JSON: %w", err)
	}

	txns := make([]models.Transaction, 0, len(records))
	var errs []error
	for i, r := range records {
		date, err := validateGatewayRecord(r)
		if err != nil {
			errs = append(errs, fmt.Errorf("gateway[%d] (payment_id=%q): %w", i, r.PaymentID, err))
			continue
		}
		txns = append(txns, models.Transaction{
			ID:           "GATEWAY:" + r.PaymentID,
			Source:       models.SourceGateway,
			OrderID:      r.OrderID,
			PaymentID:    r.PaymentID,
			SettlementID: r.SettlementID,
			GrossAmount:  r.GrossAmount,
			Fee:          r.Fee,
			Tax:          r.Tax,
			RefundAmount: r.RefundAmount,
			NetAmount:    r.NetAmount,
			Date:         date,
		})
	}

	if len(errs) > 0 {
		return txns, fmt.Errorf("ingest: %d invalid gateway record(s): %w", len(errs), errors.Join(errs...))
	}
	return txns, nil
}

func validateGatewayRecord(r gatewayRecord) (time.Time, error) {
	if r.PaymentID == "" {
		return time.Time{}, errors.New("payment_id is required")
	}
	if r.OrderID == "" {
		return time.Time{}, errors.New("order_id is required")
	}
	if r.GrossAmount <= 0 {
		return time.Time{}, fmt.Errorf("gross_amount must be positive, got %d", r.GrossAmount)
	}
	if r.Fee < 0 || r.Tax < 0 || r.RefundAmount < 0 {
		return time.Time{}, errors.New("fee, tax, and refund_amount must not be negative")
	}
	if r.NetAmount <= 0 {
		return time.Time{}, fmt.Errorf("net_amount must be positive, got %d", r.NetAmount)
	}
	if want := r.GrossAmount - r.Fee - r.Tax - r.RefundAmount; r.NetAmount != want {
		return time.Time{}, fmt.Errorf("net_amount %d does not equal gross-fee-tax-refund (%d)", r.NetAmount, want)
	}
	if r.SettlementID == "" {
		return time.Time{}, errors.New("settlement_id is required")
	}
	if r.SettlementDate == "" {
		return time.Time{}, errors.New("settlement_date is required")
	}
	date, err := time.Parse(dateLayout, r.SettlementDate)
	if err != nil {
		return time.Time{}, fmt.Errorf("settlement_date %q is invalid: %w", r.SettlementDate, err)
	}
	return date, nil
}
