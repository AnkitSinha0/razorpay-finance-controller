// Package models holds the canonical Transaction struct that all three
// input sources (ledger, gateway, bank) normalize into. See CLAUDE.md
// Phase 1.
package models

import "time"

// Source identifies which input system a Transaction was normalized from.
type Source string

const (
	SourceLedger  Source = "LEDGER"
	SourceGateway Source = "GATEWAY"
	SourceBank    Source = "BANK"
)

// Transaction is the canonical representation of a single record from
// any of the three sources, after normalization. All money fields are
// integer paise — never float. Fields that don't apply to a given
// Source are left at their zero value (e.g. a bank Transaction has no
// OrderID).
type Transaction struct {
	ID           string
	Source       Source
	OrderID      string
	PaymentID    string
	SettlementID string
	UTR          string
	Customer     string
	GrossAmount  int64
	Fee          int64
	Tax          int64
	RefundAmount int64
	NetAmount    int64
	CreditAmount int64
	Date         time.Time
	Description  string
	Status       string
}
