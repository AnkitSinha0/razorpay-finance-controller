package ingest

import "testing"

func TestLoadBank_Valid(t *testing.T) {
	path := writeTempJSON(t, "bank.json", `[
		{"utr": "ABC123", "date": "2026-09-03", "credit_amount": 978800, "description": "RAZORPAY PAY_1001"}
	]`)

	txns, err := LoadBank(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txns) != 1 {
		t.Fatalf("got %d transactions, want 1", len(txns))
	}
	got := txns[0]
	if got.UTR != "ABC123" || got.CreditAmount != 978800 {
		t.Errorf("unexpected transaction: %+v", got)
	}
	if got.Date.Format(dateLayout) != "2026-09-03" {
		t.Errorf("Date = %v, want 2026-09-03", got.Date)
	}
}

func TestLoadBank_Invalid(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{"missing utr", `[{"date": "2026-09-03", "credit_amount": 100, "description": "x"}]`},
		{"zero credit_amount", `[{"utr": "U1", "date": "2026-09-03", "credit_amount": 0, "description": "x"}]`},
		{"missing description", `[{"utr": "U1", "date": "2026-09-03", "credit_amount": 100}]`},
		{"missing date", `[{"utr": "U1", "credit_amount": 100, "description": "x"}]`},
		{"malformed date", `[{"utr": "U1", "date": "03-09-2026", "credit_amount": 100, "description": "x"}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempJSON(t, "bank.json", tt.json)
			_, err := LoadBank(path)
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}
