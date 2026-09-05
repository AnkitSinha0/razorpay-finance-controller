package judge

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type fakeProvider struct {
	verdict Verdict
	err     error
}

func (f fakeProvider) Judge(ctx context.Context, req Request) (Verdict, error) {
	return f.verdict, f.err
}

func TestJudgeAll_PassesThroughValidVerdict(t *testing.T) {
	want := Verdict{OrderID: "ORD_1", Decision: Unresolved, Reason: "no distinguishing evidence", Confidence: 0.9, RiskIfWrong: "LOW"}
	got := JudgeAll(context.Background(), fakeProvider{verdict: want}, []Request{{OrderID: "ORD_1"}})

	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("got %+v, want [%+v]", got, want)
	}
	if got[0].Fallback {
		t.Error("Fallback = true for a genuine Gemini verdict, want false")
	}
}

// TestJudgeAll_FallsBackToUnresolvedOnProviderError also confirms
// Fallback is set: the dashboard uses this to distinguish "Gemini
// looked and genuinely couldn't tell" from "Gemini's answer was thrown
// out unread" — both surface as Decision=UNRESOLVED, but they should
// never be presented to a user as the same story.
func TestJudgeAll_FallsBackToUnresolvedOnProviderError(t *testing.T) {
	got := JudgeAll(context.Background(), fakeProvider{err: errors.New("network exploded")}, []Request{{OrderID: "ORD_1"}})

	if len(got) != 1 {
		t.Fatalf("got %d verdicts, want 1", len(got))
	}
	if got[0].Decision != Unresolved {
		t.Errorf("Decision = %s, want UNRESOLVED", got[0].Decision)
	}
	if got[0].OrderID != "ORD_1" {
		t.Errorf("OrderID = %s, want ORD_1", got[0].OrderID)
	}
	if !got[0].Fallback {
		t.Error("Fallback = false for a provider error, want true")
	}
}

func TestValidateRaw(t *testing.T) {
	req := Request{
		OrderID: "ORD_1",
		Candidates: []Candidate{
			{UTR: "UTR_A", CreditAmount: 100},
			{UTR: "UTR_B", CreditAmount: 100},
		},
	}

	tests := []struct {
		name        string
		decision    string
		matchedIDs  []string
		reason      string
		confidence  float64
		riskIfWrong string
		wantErr     bool
	}{
		{"valid MATCHED", "MATCHED", []string{"UTR_A"}, "clear reference match", 0.95, "LOW", false},
		{"valid UNRESOLVED, no ids", "UNRESOLVED", nil, "no distinguishing evidence between UTR_A and UTR_B", 0.5, "MEDIUM", false},
		{"invalid decision enum", "MAYBE", nil, "reason", 0.5, "LOW", true},
		{"MATCHED with no matched_ids", "MATCHED", nil, "reason", 0.9, "LOW", true},
		{"matched_id not in candidate list", "MATCHED", []string{"UTR_ZZZ"}, "reason", 0.9, "LOW", true},
		{"empty reason", "UNRESOLVED", nil, "", 0.5, "LOW", true},
		{"confidence out of range high", "UNRESOLVED", nil, "reason", 1.5, "LOW", true},
		{"confidence out of range negative", "UNRESOLVED", nil, "reason", -0.1, "LOW", true},
		{"confidence exactly zero is rejected as meaningless", "UNRESOLVED", nil, "reason", 0, "LOW", true},
		{"empty risk_if_wrong", "UNRESOLVED", nil, "reason", 0.5, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := ValidateRaw(req, tt.decision, tt.matchedIDs, tt.reason, tt.confidence, tt.riskIfWrong)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v.OrderID != req.OrderID {
				t.Errorf("OrderID = %s, want %s", v.OrderID, req.OrderID)
			}
		})
	}
}
