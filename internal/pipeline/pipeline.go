// Package pipeline wires ingest -> deterministic match -> (optional) AI
// judge -> exception classification -> metrics into one reconciliation
// run. It is the only place that orchestrates the full flow, so
// internal/api's HTTP handlers can stay thin wrappers around it
// (CLAUDE.md Implementation Rule 3: business logic stays out of
// handlers; Rule 5: ingestion stays separate from reconciliation logic).
package pipeline

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"razorpay_finance_controller/internal/exceptions"
	"razorpay_finance_controller/internal/ingest"
	"razorpay_finance_controller/internal/judge"
	"razorpay_finance_controller/internal/match"
	"razorpay_finance_controller/internal/metrics"
)

// Config configures one reconciliation run.
type Config struct {
	// FixturesDir holds ledger.json, gateway.json, bank.json, and
	// (optionally) ground_truth.json.
	FixturesDir string
	// Judge is optional. If nil, ambiguous orders are reported as
	// pending rather than sent to an AI judge — a legitimate, honest
	// mode (e.g. no Vertex AI credentials configured), not a failure.
	Judge judge.Provider
}

// Report is the full result of one reconciliation run.
type Report struct {
	GeneratedAt time.Time              `json:"generated_at"`
	Summary     metrics.Summary        `json:"summary"`
	Results     []match.Result         `json:"results"`
	Verdicts    []judge.Verdict        `json:"verdicts,omitempty"`
	Exceptions  []exceptions.Exception `json:"exceptions"`
}

// Run executes the full pipeline against Config.FixturesDir.
func Run(ctx context.Context, cfg Config) (Report, error) {
	batch, err := ingest.LoadAll(cfg.FixturesDir)
	if err != nil {
		return Report{}, fmt.Errorf("pipeline: ingest: %w", err)
	}

	results := match.Run(batch)

	var verdicts []judge.Verdict
	if cfg.Judge != nil {
		reqs := judge.BuildRequests(results, batch.Bank)
		if len(reqs) > 0 {
			verdicts = judge.JudgeAll(ctx, cfg.Judge, reqs)
		}
	}

	asOf := latestDate(batch)
	excs := exceptions.Build(results, verdicts, batch.Gateway, batch.Ledger, asOf)

	// ground_truth.json is a synthetic-data evaluation aid (Phase 0); a
	// production fixtures directory simply won't have one, and that's
	// fine — LoadGroundTruth's error is intentionally ignored here.
	groundTruth, _ := metrics.LoadGroundTruth(filepath.Join(cfg.FixturesDir, "ground_truth.json"))
	summary := metrics.Compute(results, verdicts, groundTruth)

	return Report{
		GeneratedAt: time.Now(),
		Summary:     summary,
		Results:     results,
		Verdicts:    verdicts,
		Exceptions:  excs,
	}, nil
}

// latestDate finds the most recent settlement/credit date in the batch,
// used as the exception engine's "as of" reference for STALE_SETTLEMENT
// classification. This keeps that classification anchored to the data's
// own timeline instead of the real wall clock, which would be
// meaningless against synthetic dates (CLAUDE.md Implementation Rule 7:
// deterministic decisions must be reproducible).
func latestDate(batch ingest.Batch) time.Time {
	var latest time.Time
	for _, g := range batch.Gateway {
		if g.Date.After(latest) {
			latest = g.Date
		}
	}
	for _, b := range batch.Bank {
		if b.Date.After(latest) {
			latest = b.Date
		}
	}
	return latest
}
