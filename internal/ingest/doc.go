// Package ingest parses the three raw input sources (ledger, gateway,
// bank — JSON fixtures under data/fixtures/) and normalizes each into
// models.Transaction. Every field is validated on ingest; a record that
// fails validation is reported, never silently dropped or coerced. See
// CLAUDE.md Phase 0/1.
package ingest

const dateLayout = "2006-01-02"
