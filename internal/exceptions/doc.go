// Package exceptions classifies every non-MATCHED record
// (MISSING_IN_BANK, POSSIBLE_DUPLICATE, FEE_MISMATCH, STALE_SETTLEMENT),
// assigns a recommended action (RECHECK, MANUAL_REVIEW, VERIFY_FEE,
// ESCALATE), and ranks results by value at risk. See CLAUDE.md Phase 4.
package exceptions
