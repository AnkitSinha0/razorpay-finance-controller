package ingest

import "testing"

func TestLoadGateway_Valid(t *testing.T) {
	path := writeTempJSON(t, "gateway.json", `[
		{"payment_id": "PAY_1001", "order_id": "ORD_1001", "gross_amount": 1000000,
		 "fee": 18000, "tax": 3240, "net_amount": 978760, "settlement_id": "ST_501",
		 "settlement_date": "2026-09-03"}
	]`)

	txns, err := LoadGateway(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txns) != 1 {
		t.Fatalf("got %d transactions, want 1", len(txns))
	}
	got := txns[0]
	if got.NetAmount != 978760 || got.SettlementID != "ST_501" {
		t.Errorf("unexpected transaction: %+v", got)
	}
	if got.Date.Format(dateLayout) != "2026-09-03" {
		t.Errorf("Date = %v, want 2026-09-03", got.Date)
	}
}

func TestLoadGateway_RefundAccountedForInNet(t *testing.T) {
	path := writeTempJSON(t, "gateway.json", `[
		{"payment_id": "PAY_1001", "order_id": "ORD_1001", "gross_amount": 1000000,
		 "fee": 18000, "tax": 3240, "refund_amount": 50000, "net_amount": 928760,
		 "settlement_id": "ST_501", "settlement_date": "2026-09-03"}
	]`)

	txns, err := LoadGateway(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if txns[0].RefundAmount != 50000 {
		t.Errorf("RefundAmount = %d, want 50000", txns[0].RefundAmount)
	}
}

func TestLoadGateway_Invalid(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{"missing payment_id", `[{"order_id": "ORD_1", "gross_amount": 100, "fee": 1, "tax": 1, "net_amount": 98, "settlement_id": "ST_1", "settlement_date": "2026-09-03"}]`},
		{"missing order_id", `[{"payment_id": "PAY_1", "gross_amount": 100, "fee": 1, "tax": 1, "net_amount": 98, "settlement_id": "ST_1", "settlement_date": "2026-09-03"}]`},
		{"negative fee", `[{"payment_id": "PAY_1", "order_id": "ORD_1", "gross_amount": 100, "fee": -1, "tax": 1, "net_amount": 100, "settlement_id": "ST_1", "settlement_date": "2026-09-03"}]`},
		{"net_amount inconsistent with gross-fee-tax", `[{"payment_id": "PAY_1", "order_id": "ORD_1", "gross_amount": 1000000, "fee": 18000, "tax": 3240, "net_amount": 999999, "settlement_id": "ST_1", "settlement_date": "2026-09-03"}]`},
		{"missing settlement_id", `[{"payment_id": "PAY_1", "order_id": "ORD_1", "gross_amount": 100, "fee": 1, "tax": 1, "net_amount": 98, "settlement_date": "2026-09-03"}]`},
		{"missing settlement_date", `[{"payment_id": "PAY_1", "order_id": "ORD_1", "gross_amount": 100, "fee": 1, "tax": 1, "net_amount": 98, "settlement_id": "ST_1"}]`},
		{"malformed settlement_date", `[{"payment_id": "PAY_1", "order_id": "ORD_1", "gross_amount": 100, "fee": 1, "tax": 1, "net_amount": 98, "settlement_id": "ST_1", "settlement_date": "09/03/2026"}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempJSON(t, "gateway.json", tt.json)
			_, err := LoadGateway(path)
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}
