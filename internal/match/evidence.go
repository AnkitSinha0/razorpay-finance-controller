package match

import (
	"math"
	"sort"
	"strings"
	"time"
)

// Component weights for EvidenceBreakdown. They sum to 100 so Total is
// always a 0-100 score, same range as the single evidence_score figure
// it replaces as the source of truth.
const (
	MaxAmountScore      = 40.0
	MaxDateScore        = 15.0
	MaxReferenceScore   = 30.0
	MaxDescriptionScore = 10.0
	MaxUniquenessScore  = 5.0
)

// EvidenceBreakdown is the per-component evidence score for one tied
// AMBIGUOUS candidate — the auditable calculation behind Phase 9's
// single evidence_score, so the dashboard can show the math instead of
// one opaque number (Precision & Impressiveness Pass, Priority 1).
// Every component is independently 0 up to its own max; Total is their
// sum, 0-100.
type EvidenceBreakdown struct {
	AmountScore      float64 `json:"amount_score"`
	DateScore        float64 `json:"date_score"`
	ReferenceScore   float64 `json:"reference_score"`
	DescriptionScore float64 `json:"description_score"`
	UniquenessScore  float64 `json:"uniqueness_score"`
	Total            float64 `json:"total"`
}

// CandidateEvidence is one tied bank candidate recorded for an AMBIGUOUS
// result. Score is the compact 0-100 summary used where one number is
// more useful than five (e.g. the Gemini prompt); Breakdown is the full
// component calculation it's derived from — Score is always
// Breakdown.Total rounded, never a separately-tuned value, so the two
// can never drift apart.
type CandidateEvidence struct {
	UTR       string            `json:"utr"`
	Score     int               `json:"evidence_score"`
	Breakdown EvidenceBreakdown `json:"breakdown"`
}

// evidenceBreakdown scores one tied candidate across five independent
// components: how closely its amount matches (out of 40), how closely
// its date matches within the tolerance window (out of 15), how closely
// the bank description resembles the expected payment reference (out of
// 30, continuous — not just present/absent), whether it merely looks
// like a plausible settlement credit in general — e.g. mentions
// "RAZORPAY" or "SETTLEMENT" without naming a specific payment (out of
// 10), and whether this candidate's description is even distinguishable
// from the other candidates tied to the same order (out of 5).
//
// Amount, date, and reference are all continuous ratios rather than
// step functions (Precision & Impressiveness Pass, Priority 3) — two
// genuinely different candidates should resolve to genuinely different
// decimal totals (69.4 vs 68.7), not round coincidences that read as
// hardcoded. When every input is truly identical between candidates —
// same amount, same day, byte-identical description (the DUPLICATE
// fixture case) — the totals still tie exactly, and that's correct: it
// means the data offers no basis to prefer one over the other, which
// match.Run calls out explicitly in Notes rather than leaving an
// unexplained coincidence on screen.
func evidenceBreakdown(amountDeltaPaise int64, dateDelta time.Duration, description, paymentID string, unique bool) EvidenceBreakdown {
	if amountDeltaPaise < 0 {
		amountDeltaPaise = -amountDeltaPaise
	}
	if dateDelta < 0 {
		dateDelta = -dateDelta
	}

	dateRatio := clamp(1-dateDelta.Hours()/dateWindow.Hours(), 0, 1)

	b := EvidenceBreakdown{
		AmountScore:    round2(clamp(MaxAmountScore-float64(amountDeltaPaise)/100, 0, MaxAmountScore)),
		DateScore:      round2(MaxDateScore * dateRatio),
		ReferenceScore: round2(MaxReferenceScore * referenceSimilarity(description, paymentID)),
	}
	if looksLikeSettlementCredit(description) {
		b.DescriptionScore = MaxDescriptionScore
	}
	if unique {
		b.UniquenessScore = MaxUniquenessScore
	}
	b.Total = round2(b.AmountScore + b.DateScore + b.ReferenceScore + b.DescriptionScore + b.UniquenessScore)
	return b
}

// round2 rounds to 2 decimal places — enough for the dashboard to show
// a genuine fraction (69.4, not 69.39999999999999) without floating-
// point arithmetic artifacts breaking equality comparisons or reading
// as noise.
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// evidenceScore is evidenceBreakdown's Total, rounded to the nearest
// integer — the compact figure used wherever one number is more useful
// than five (the Gemini prompt, mainly). Deliberately not an
// independent calculation: it can never disagree with the breakdown
// because it's computed from it.
func evidenceScore(amountDeltaPaise int64, dateDelta time.Duration, description, paymentID string, unique bool) int {
	return int(math.Round(evidenceBreakdown(amountDeltaPaise, dateDelta, description, paymentID, unique).Total))
}

// referenceSimilarity returns a 0-1 ratio for how closely description
// resembles the expected payment reference: 1.0 when it contains the
// exact payment ID, a partial ratio (longest common substring / len of
// the ID) when the overlap is substantial, 0 when there's no payment ID
// to compare against or the overlap is too short to be meaningful.
// Continuous rather than boolean so a near-miss (a truncated or
// lightly-mangled reference) scores between "exact" and "nothing",
// instead of collapsing to the same 0 as a wholly unrelated
// description.
//
// The "substantial" floor matters: "RAZORPAY" itself contains "PAY" as
// a substring, which would otherwise coincidentally overlap the start
// of every "PAY_xxxx" ID and hand a generic, reference-free settlement
// description partial credit it hasn't earned. Requiring the overlap to
// cover at least half of the payment ID's length rejects that
// coincidence while still rewarding genuine near-misses (a truncated
// "PAY_105" against "PAY_1050" overlaps 7 of 8 characters).
func referenceSimilarity(description, paymentID string) float64 {
	if paymentID == "" {
		return 0
	}
	d := strings.ToUpper(description)
	p := strings.ToUpper(paymentID)
	if strings.Contains(d, p) {
		return 1
	}
	common := longestCommonSubstringLen(d, p)
	if common*2 < len(p) {
		return 0
	}
	return clamp(float64(common)/float64(len(p)), 0, 1)
}

// longestCommonSubstringLen returns the length of the longest
// contiguous substring shared by a and b (classic O(len(a)*len(b)) DP;
// both inputs here are short bank-description/payment-ID strings).
func longestCommonSubstringLen(a, b string) int {
	prev := make([]int, len(b)+1)
	best := 0
	for i := 1; i <= len(a); i++ {
		curr := make([]int, len(b)+1)
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				curr[j] = prev[j-1] + 1
				if curr[j] > best {
					best = curr[j]
				}
			}
		}
		prev = curr
	}
	return best
}

// looksLikeSettlementCredit is a weaker signal than an exact payment
// reference: the description merely reads like a plausible Razorpay
// settlement credit in general, without naming a specific payment.
func looksLikeSettlementCredit(description string) bool {
	d := strings.ToUpper(description)
	return strings.Contains(d, "RAZORPAY") || strings.Contains(d, "SETTLEMENT")
}

// decisionConfidenceScale converts the evidence-score margin between
// the top two tied candidates into a 0-100 "decision confidence". A
// margin of 25 points or more fully saturates it to 100; the scale was
// picked so a 0.7-point margin (two genuinely-tied candidates, decimal
// scores) reads as ~3 — a small, honest number, not a rounding blip.
const decisionConfidenceScale = 4.0

// decisionConfidence reports how clearly the leading candidate (by
// evidenceBreakdown Total) beats the runner-up, scaled 0-100. It is
// deliberately distinct from either candidate's own evidence_score: two
// candidates can each individually score reasonably (say 70 and 69.4)
// while the *decision* between them is barely-there — a 0.6-point
// margin maps to a low decision confidence, which is what should drive
// MANUAL_REVIEW, not either candidate's absolute score. Requires at
// least two totals; an AMBIGUOUS result always has 2+ tied candidates
// by construction, so this only ever runs where it's meaningful.
func decisionConfidence(totals []float64) float64 {
	if len(totals) < 2 {
		return 100
	}
	sorted := append([]float64(nil), totals...)
	sort.Sort(sort.Reverse(sort.Float64Slice(sorted)))
	margin := sorted[0] - sorted[1]
	return clamp(margin*decisionConfidenceScale, 0, 100)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
