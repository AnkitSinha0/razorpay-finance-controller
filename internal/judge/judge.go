// Package judge sends only the ambiguous slice (plus its candidates)
// to Gemini and returns a validated decision
// (MATCHED/DISCREPANCY/UNRESOLVED). The provider is isolated behind an
// interface here so it can be swapped or mocked without touching the
// matching engine (CLAUDE.md Implementation Rule 4). LLM output is
// untrusted until validated — a provider error or an invalid response
// always degrades to UNRESOLVED, never to a guessed MATCHED. See
// CLAUDE.md Phase 3.
package judge

import (
	"context"
	"fmt"
)

// Decision mirrors match.Decision's terminal outcomes; the judge never
// returns AMBIGUOUS — it either resolves the case or, honestly, cannot.
type Decision string

const (
	Matched     Decision = "MATCHED"
	Discrepancy Decision = "DISCREPANCY"
	Unresolved  Decision = "UNRESOLVED"
)

// Candidate is one bank credit the judge is allowed to choose between.
type Candidate struct {
	UTR          string
	CreditAmount int64
	Date         string
	Description  string
	// EvidenceScore is the deterministic engine's own 0-100 score for
	// this candidate (match.CandidateEvidence, Phase 9), computed from
	// amount/date/description matching before the judge ever sees the
	// request. It is shown to Gemini as one input alongside its own
	// reasoning, never as a substitute for it.
	EvidenceScore int
}

// Request is everything the judge sees for one ambiguous order. It sees
// only this slice — never the full dataset (CLAUDE.md architecture:
// "sees ONLY the ambiguous slice + its candidates").
type Request struct {
	OrderID      string
	PaymentID    string
	SettlementID string
	ExpectedNet  int64
	Candidates   []Candidate
}

// Verdict is the judge's validated decision for one Request.
type Verdict struct {
	OrderID     string   `json:"order_id"`
	Decision    Decision `json:"decision"`
	MatchedIDs  []string `json:"matched_ids,omitempty"`
	Reason      string   `json:"reason"`
	Confidence  float64  `json:"confidence"`
	RiskIfWrong string   `json:"risk_if_wrong"`
	// Fallback is true when this Verdict was never actually produced by
	// Gemini — the provider errored, or its response was rejected by
	// ValidateRaw — and JudgeAll substituted this placeholder instead.
	// Decision is always Unresolved when Fallback is true, but callers
	// (the dashboard especially) should key off this field rather than
	// Decision/Reason text matching: it's the difference between "Gemini
	// looked and couldn't tell" and "Gemini's answer was thrown out
	// unread". The underlying match.Result.Decision (still AMBIGUOUS)
	// is the authority on whether the order needs manual review either
	// way — this only describes what happened to the AI attempt.
	Fallback bool `json:"fallback,omitempty"`
}

// Provider is implemented by any LLM backend that can judge an
// ambiguous reconciliation case.
type Provider interface {
	Judge(ctx context.Context, req Request) (Verdict, error)
}

// JudgeAll runs Provider.Judge over every request. A provider error
// (network failure, malformed output, a validation failure) never
// propagates as a crash and never becomes a guessed MATCHED — it
// degrades to a Verdict with Decision Unresolved explaining why.
func JudgeAll(ctx context.Context, p Provider, reqs []Request) []Verdict {
	out := make([]Verdict, 0, len(reqs))
	for _, req := range reqs {
		v, err := p.Judge(ctx, req)
		if err != nil {
			out = append(out, fallbackUnresolved(req, err))
			continue
		}
		out = append(out, v)
	}
	return out
}

// ValidateRaw checks a provider's decoded-but-untrusted output against
// the Request it answered, and only then produces a Verdict. Any
// provider implementation should route its parsed response through
// this before returning — it is the single place the "never trust LLM
// output" rule is enforced (CLAUDE.md Implementation Rule 8).
func ValidateRaw(req Request, decision string, matchedIDs []string, reason string, confidence float64, riskIfWrong string) (Verdict, error) {
	d := Decision(decision)
	switch d {
	case Matched, Discrepancy, Unresolved:
	default:
		return Verdict{}, fmt.Errorf("invalid decision %q", decision)
	}

	candidateUTRs := make(map[string]bool, len(req.Candidates))
	for _, c := range req.Candidates {
		candidateUTRs[c.UTR] = true
	}
	for _, id := range matchedIDs {
		if !candidateUTRs[id] {
			return Verdict{}, fmt.Errorf("matched_ids contains %q, which was not offered as a candidate", id)
		}
	}
	if d == Matched && len(matchedIDs) == 0 {
		return Verdict{}, fmt.Errorf("decision MATCHED requires at least one matched_id")
	}
	if reason == "" {
		return Verdict{}, fmt.Errorf("reason is required")
	}
	// Strictly greater than 0, not just "in range": a confidence of
	// exactly 0 is not a meaningful signal (found live — Gemini returned
	// a fully-formed UNRESOLVED verdict with confidence:0 for the
	// DUPLICATE fixture, which the old inclusive [0,1] check accepted as
	// valid and the dashboard then rendered as an indistinguishable-from-
	// broken flat 0% bar). Treated the same as any other malformed
	// response: rejected here, degrading to UNRESOLVED via
	// fallbackUnresolved rather than displaying a meaningless zero as if
	// it were real data.
	if confidence <= 0 || confidence > 1 {
		return Verdict{}, fmt.Errorf("confidence %v is out of range (0,1]", confidence)
	}
	if riskIfWrong == "" {
		return Verdict{}, fmt.Errorf("risk_if_wrong is required")
	}

	return Verdict{
		OrderID: req.OrderID, Decision: d, MatchedIDs: matchedIDs,
		Reason: reason, Confidence: confidence, RiskIfWrong: riskIfWrong,
	}, nil
}

func fallbackUnresolved(req Request, err error) Verdict {
	return Verdict{
		OrderID:     req.OrderID,
		Decision:    Unresolved,
		Reason:      fmt.Sprintf("AI judge output was rejected or unavailable: %v", err),
		Confidence:  0,
		RiskIfWrong: "UNKNOWN — could not be evaluated by the AI judge",
		Fallback:    true,
	}
}
