package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"google.golang.org/genai"
)

const defaultVertexModel = "gemini-2.5-flash"

// GeminiProvider judges ambiguous reconciliation cases using Gemini
// via Vertex AI. It is the only production Provider implementation
// (CLAUDE.md: single LLM provider, isolated behind the Provider
// interface). Retries with bounded exponential backoff on 408/429/5xx
// are handled by the underlying genai SDK client.
type GeminiProvider struct {
	Client *genai.Client
	Model  string
}

// NewGeminiProvider builds a GeminiProvider backed by Vertex AI in the
// given GCP project/location. apiKey may be empty, in which case the
// client falls back to Application Default Credentials.
func NewGeminiProvider(ctx context.Context, project, location, apiKey string) (*GeminiProvider, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Backend:  genai.BackendVertexAI,
		Project:  project,
		Location: location,
		APIKey:   apiKey,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini: create vertex ai client: %w", err)
	}
	return &GeminiProvider{Client: client, Model: defaultVertexModel}, nil
}

// NewGeminiProviderFromEnv reads GOOGLE_CLOUD_PROJECT and
// GOOGLE_CLOUD_LOCATION (both required for Vertex AI), GOOGLE_API_KEY
// (optional — omit to use Application Default Credentials instead), and
// GEMINI_MODEL (optional override).
func NewGeminiProviderFromEnv(ctx context.Context) (*GeminiProvider, error) {
	project := os.Getenv("GOOGLE_CLOUD_PROJECT")
	location := os.Getenv("GOOGLE_CLOUD_LOCATION")
	if project == "" || location == "" {
		return nil, fmt.Errorf("GOOGLE_CLOUD_PROJECT and GOOGLE_CLOUD_LOCATION are required")
	}
	p, err := NewGeminiProvider(ctx, project, location, os.Getenv("GOOGLE_API_KEY"))
	if err != nil {
		return nil, err
	}
	if model := os.Getenv("GEMINI_MODEL"); model != "" {
		p.Model = model
	}
	return p, nil
}

// verdictSchema forces the model's structured output into exactly the
// shape ValidateRaw expects.
var verdictSchema = &genai.Schema{
	Type: genai.TypeObject,
	Properties: map[string]*genai.Schema{
		"decision":      {Type: genai.TypeString, Format: "enum", Enum: []string{"MATCHED", "DISCREPANCY", "UNRESOLVED"}},
		"matched_ids":   {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
		"reason":        {Type: genai.TypeString},
		"confidence":    {Type: genai.TypeNumber},
		"risk_if_wrong": {Type: genai.TypeString},
	},
	Required: []string{"decision", "matched_ids", "reason", "confidence", "risk_if_wrong"},
}

type rawVerdict struct {
	Decision    string   `json:"decision"`
	MatchedIDs  []string `json:"matched_ids"`
	Reason      string   `json:"reason"`
	Confidence  float64  `json:"confidence"`
	RiskIfWrong string   `json:"risk_if_wrong"`
}

// Judge implements Provider.
func (p *GeminiProvider) Judge(ctx context.Context, req Request) (Verdict, error) {
	config := &genai.GenerateContentConfig{
		Temperature:      genai.Ptr(float32(0)),
		ResponseMIMEType: "application/json",
		ResponseSchema:   verdictSchema,
	}

	resp, err := p.Client.Models.GenerateContent(ctx, p.Model, genai.Text(buildPrompt(req)), config)
	if err != nil {
		return Verdict{}, fmt.Errorf("gemini: generate content: %w", err)
	}

	text := resp.Text()
	if text == "" {
		return Verdict{}, fmt.Errorf("gemini: response had no text content")
	}

	var raw rawVerdict
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return Verdict{}, fmt.Errorf("gemini: response was not valid JSON: %w (raw: %s)", err, truncate(text, 200))
	}

	return ValidateRaw(req, raw.Decision, raw.MatchedIDs, raw.Reason, raw.Confidence, raw.RiskIfWrong)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func buildPrompt(req Request) string {
	var b strings.Builder
	b.WriteString("You are a finance reconciliation judge for a payments system. ")
	b.WriteString("The deterministic matching engine could not confidently resolve one order because ")
	b.WriteString("multiple bank credits are equally plausible matches (or none are). ")
	b.WriteString("Decide MATCHED (exactly one candidate is clearly the right one, with a reason), ")
	b.WriteString("DISCREPANCY (evidence ties this to a specific candidate but the amount doesn't fully reconcile), ")
	b.WriteString("or UNRESOLVED (the evidence is genuinely insufficient to pick one — this is a correct, expected, ")
	b.WriteString("and often the right answer; never guess). ")
	b.WriteString("If you choose MATCHED or DISCREPANCY, matched_ids must contain only UTR(s) from the candidate list below — ")
	b.WriteString("never invent an ID. Be conservative: picking the wrong match on real money is worse than saying UNRESOLVED. ")
	b.WriteString("Each candidate lists an evidence_score (0-100), already computed deterministically from amount/date/description ")
	b.WriteString("matching before you saw this request. Use it as one input alongside your own reasoning, not as a substitute for it — ")
	b.WriteString("two candidates tied on evidence_score genuinely have no distinguishing evidence, and UNRESOLVED is correct.\n\n")

	fmt.Fprintf(&b, "Order: %s\n", req.OrderID)
	if req.PaymentID != "" {
		fmt.Fprintf(&b, "Payment ID: %s\n", req.PaymentID)
	}
	if req.SettlementID != "" {
		fmt.Fprintf(&b, "Settlement ID: %s\n", req.SettlementID)
	}
	fmt.Fprintf(&b, "Expected net amount (paise): %d\n\n", req.ExpectedNet)

	b.WriteString("Candidate bank credits:\n")
	for _, c := range req.Candidates {
		fmt.Fprintf(&b, "- utr=%s amount=%d date=%s description=%q evidence_score=%d\n", c.UTR, c.CreditAmount, c.Date, c.Description, c.EvidenceScore)
	}

	b.WriteString("\nRespond with the required JSON fields only.")
	return b.String()
}
