package ingest

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempJSON(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadLedger_Valid(t *testing.T) {
	path := writeTempJSON(t, "ledger.json", `[
		{"order_id": "ORD_1001", "customer": "Amit", "gross_amount": 1000000, "status": "PAID"}
	]`)

	txns, err := LoadLedger(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txns) != 1 {
		t.Fatalf("got %d transactions, want 1", len(txns))
	}
	got := txns[0]
	if got.OrderID != "ORD_1001" || got.Customer != "Amit" || got.GrossAmount != 1000000 || got.Status != "PAID" {
		t.Errorf("unexpected transaction: %+v", got)
	}
	if got.Source != "LEDGER" {
		t.Errorf("Source = %q, want LEDGER", got.Source)
	}
}

func TestLoadLedger_Invalid(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{"missing order_id", `[{"customer": "Amit", "gross_amount": 1000000, "status": "PAID"}]`},
		{"missing customer", `[{"order_id": "ORD_1", "gross_amount": 1000000, "status": "PAID"}]`},
		{"zero gross_amount", `[{"order_id": "ORD_1", "customer": "Amit", "gross_amount": 0, "status": "PAID"}]`},
		{"negative gross_amount", `[{"order_id": "ORD_1", "customer": "Amit", "gross_amount": -500, "status": "PAID"}]`},
		{"missing status", `[{"order_id": "ORD_1", "customer": "Amit", "gross_amount": 1000000}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempJSON(t, "ledger.json", tt.json)
			_, err := LoadLedger(path)
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}
