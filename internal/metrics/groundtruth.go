package metrics

import (
	"encoding/json"
	"fmt"
	"os"
)

// GroundTruthEntry mirrors one record of Phase 0's ground_truth.json —
// the hand-verified expected outcome for one synthetic order. It exists
// only to make the accuracy claim real instead of asserted; production
// data has no such file.
type GroundTruthEntry struct {
	OrderID          string `json:"order_id"`
	CaseType         string `json:"case_type"`
	ExpectedDecision string `json:"expected_decision"`
}

// LoadGroundTruth reads ground_truth.json. A missing file is not an
// error — callers should treat it as "no ground truth available" and
// simply skip the accuracy metric, since production data won't have one.
func LoadGroundTruth(path string) ([]GroundTruthEntry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("metrics: read ground truth file: %w", err)
	}
	var entries []GroundTruthEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("metrics: parse ground truth JSON: %w", err)
	}
	return entries, nil
}
