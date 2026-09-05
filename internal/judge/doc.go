// Package judge sends only the ambiguous slice (plus its candidates)
// to the Claude API and returns a validated decision
// (MATCHED/DISCREPANCY/UNRESOLVED). The provider is isolated behind an
// interface here so it can be swapped or mocked without touching the
// matching engine. LLM output is untrusted until validated. See
// CLAUDE.md Phase 3.
package judge
