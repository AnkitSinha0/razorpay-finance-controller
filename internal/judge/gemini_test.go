package judge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/genai"
)

func testRequest() Request {
	return Request{
		OrderID:     "ORD_1050",
		PaymentID:   "PAY_1050",
		ExpectedNet: 489380,
		Candidates: []Candidate{
			{UTR: "UTR000048", CreditAmount: 489380, Date: "2026-10-11", Description: "RAZORPAY SETTLEMENT"},
			{UTR: "UTR000049", CreditAmount: 489380, Date: "2026-10-11", Description: "RAZORPAY SETTLEMENT"},
		},
	}
}

// geminiEnvelope wraps innerJSON the way Vertex AI wraps the model's
// structured-output text inside candidates[0].content.parts[0].text.
func geminiEnvelope(t *testing.T, innerJSON string) string {
	t.Helper()
	envelope := map[string]any{
		"candidates": []map[string]any{
			{"content": map[string]any{
				"role":  "model",
				"parts": []map[string]any{{"text": innerJSON}},
			}},
		},
	}
	b, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// newTestProvider builds a GeminiProvider whose genai.Client is pointed
// at a local httptest.Server instead of real Vertex AI, via
// HTTPOptions.BaseURL (which takes precedence over everything else per
// the SDK's own base_url.go). maxRetries, if > 0, bounds retry attempts
// for the retry-behavior tests.
func newTestProvider(t *testing.T, url string, maxRetries int32) *GeminiProvider {
	t.Helper()
	cfg := &genai.ClientConfig{
		Backend:  genai.BackendVertexAI,
		Project:  "test-project",
		Location: "us-central1",
		APIKey:   "test-key",
		HTTPOptions: genai.HTTPOptions{
			BaseURL: url,
		},
	}
	if maxRetries > 0 {
		cfg.HTTPOptions.RetryOptions = &genai.HTTPRetryOptions{Attempts: genai.Ptr(maxRetries)}
	}
	client, err := genai.NewClient(context.Background(), cfg)
	if err != nil {
		t.Fatalf("genai.NewClient: %v", err)
	}
	return &GeminiProvider{Client: client, Model: defaultVertexModel}
}

func TestGeminiProvider_Judge_Success(t *testing.T) {
	inner := `{"decision":"UNRESOLVED","matched_ids":[],"reason":"identical amount and date, no distinguishing reference","confidence":0.85,"risk_if_wrong":"MEDIUM"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-goog-api-key"); got != "test-key" {
			t.Errorf("x-goog-api-key = %q, want test-key", got)
		}
		w.Write([]byte(geminiEnvelope(t, inner)))
	}))
	defer srv.Close()

	v, err := newTestProvider(t, srv.URL, 0).Judge(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Decision != Unresolved {
		t.Errorf("Decision = %s, want UNRESOLVED", v.Decision)
	}
	if v.Confidence != 0.85 {
		t.Errorf("Confidence = %v, want 0.85", v.Confidence)
	}
}

func TestGeminiProvider_Judge_RejectsHallucinatedMatchedID(t *testing.T) {
	inner := `{"decision":"MATCHED","matched_ids":["UTR_DOES_NOT_EXIST"],"reason":"x","confidence":0.9,"risk_if_wrong":"LOW"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(geminiEnvelope(t, inner)))
	}))
	defer srv.Close()

	_, err := newTestProvider(t, srv.URL, 0).Judge(context.Background(), testRequest())
	if err == nil {
		t.Fatal("expected an error for a hallucinated matched_id, got nil")
	}
}

func TestGeminiProvider_Judge_RejectsMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(geminiEnvelope(t, "not json at all")))
	}))
	defer srv.Close()

	_, err := newTestProvider(t, srv.URL, 0).Judge(context.Background(), testRequest())
	if err == nil {
		t.Fatal("expected an error for malformed inner JSON, got nil")
	}
}

func TestGeminiProvider_Judge_RetriesOn503ThenSucceeds(t *testing.T) {
	inner := `{"decision":"UNRESOLVED","matched_ids":[],"reason":"no evidence","confidence":0.5,"risk_if_wrong":"LOW"}`
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error":{"code":503,"message":"overloaded","status":"UNAVAILABLE"}}`))
			return
		}
		w.Write([]byte(geminiEnvelope(t, inner)))
	}))
	defer srv.Close()

	v, err := newTestProvider(t, srv.URL, 5).Judge(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Decision != Unresolved {
		t.Errorf("Decision = %s, want UNRESOLVED", v.Decision)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (2 failures + 1 success)", calls)
	}
}

func TestGeminiProvider_Judge_DoesNotRetryOn400(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"code":400,"message":"bad request","status":"INVALID_ARGUMENT"}}`))
	}))
	defer srv.Close()

	_, err := newTestProvider(t, srv.URL, 3).Judge(context.Background(), testRequest())
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (400 is not retryable)", calls)
	}
}
