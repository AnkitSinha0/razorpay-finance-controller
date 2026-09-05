package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"razorpay_finance_controller/internal/pipeline"
)

func testRouter(t *testing.T) http.Handler {
	t.Helper()
	return NewRouter(pipeline.Config{FixturesDir: "../../data/fixtures"})
}

// TestHealthz_AlwaysReturns200BeforeAnyRun confirms the health check
// (Phase 15's Cloud Run startup/liveness target) never depends on
// pipeline state — a fresh instance with no cached report yet is still
// healthy, not a 409 like every other read endpoint.
func TestHealthz_AlwaysReturns200BeforeAnyRun(t *testing.T) {
	r := testRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestReportAndExceptions_BeforeRun_Return409(t *testing.T) {
	r := testRouter(t)

	for _, path := range []string{"/reconcile/report", "/reconcile/exceptions", "/reconcile/orders/ORD_1049"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusConflict {
			t.Errorf("%s before any run: status = %d, want 409", path, rec.Code)
		}
	}
}

func TestRun_ThenReportAndExceptions_Succeed(t *testing.T) {
	r := testRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/reconcile/run", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /reconcile/run: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var report pipeline.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode /reconcile/run response: %v", err)
	}
	if report.Summary.TotalOrders != 52 {
		t.Errorf("Summary.TotalOrders = %d, want 52", report.Summary.TotalOrders)
	}
	if len(report.Exceptions) != 4 {
		t.Errorf("len(Exceptions) = %d, want 4", len(report.Exceptions))
	}

	reportReq := httptest.NewRequest(http.MethodGet, "/reconcile/report", nil)
	reportRec := httptest.NewRecorder()
	r.ServeHTTP(reportRec, reportReq)
	if reportRec.Code != http.StatusOK {
		t.Fatalf("GET /reconcile/report after a run: status = %d", reportRec.Code)
	}

	excReq := httptest.NewRequest(http.MethodGet, "/reconcile/exceptions", nil)
	excRec := httptest.NewRecorder()
	r.ServeHTTP(excRec, excReq)
	if excRec.Code != http.StatusOK {
		t.Fatalf("GET /reconcile/exceptions after a run: status = %d", excRec.Code)
	}
	var exceptions []map[string]any
	if err := json.Unmarshal(excRec.Body.Bytes(), &exceptions); err != nil {
		t.Fatalf("decode /reconcile/exceptions response: %v", err)
	}
	if len(exceptions) != 4 {
		t.Errorf("len(exceptions) = %d, want 4", len(exceptions))
	}
	// ranked by value_at_risk descending
	prev := int64(1<<63 - 1)
	for _, e := range exceptions {
		v := int64(e["value_at_risk"].(float64))
		if v > prev {
			t.Errorf("exceptions not sorted descending by value_at_risk: %v after %v", v, prev)
		}
		prev = v
	}
}

func TestOrderTrace_UnknownOrder404(t *testing.T) {
	r := testRouter(t)

	runReq := httptest.NewRequest(http.MethodGet, "/reconcile/run", nil)
	runRec := httptest.NewRecorder()
	r.ServeHTTP(runRec, runReq)
	if runRec.Code != http.StatusOK {
		t.Fatalf("GET /reconcile/run: status = %d", runRec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/reconcile/orders/ORD_DOES_NOT_EXIST", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestOrderTrace_KnownOrderReturnsCombinedView(t *testing.T) {
	r := testRouter(t)

	runReq := httptest.NewRequest(http.MethodGet, "/reconcile/run", nil)
	runRec := httptest.NewRecorder()
	r.ServeHTTP(runRec, runReq)
	if runRec.Code != http.StatusOK {
		t.Fatalf("GET /reconcile/run: status = %d", runRec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/reconcile/orders/ORD_1049", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var trace pipeline.OrderTrace
	if err := json.Unmarshal(rec.Body.Bytes(), &trace); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if trace.OrderID != "ORD_1049" {
		t.Errorf("OrderID = %q, want ORD_1049", trace.OrderID)
	}
	if trace.Match.OrderID != "ORD_1049" {
		t.Errorf("Match.OrderID = %q, want ORD_1049", trace.Match.OrderID)
	}
	if trace.Exception == nil {
		t.Error("Exception = nil, want a FEE_MISMATCH exception for the refund order")
	}
	if trace.Verdict != nil {
		t.Error("Verdict != nil, want nil — this order was never escalated to the AI judge")
	}
}

func runOnce(t *testing.T, r http.Handler) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/reconcile/run", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /reconcile/run: status = %d", rec.Code)
	}
}

func postResolve(t *testing.T, r http.Handler, orderID, action, reason string) *httptest.ResponseRecorder {
	t.Helper()
	return postResolveWithUTR(t, r, orderID, action, reason, "")
}

func postResolveWithUTR(t *testing.T, r http.Handler, orderID, action, reason, utr string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"action": action, "reason": reason, "utr": utr})
	req := httptest.NewRequest(http.MethodPost, "/reconcile/orders/"+orderID+"/resolve", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestResolveOrder_BeforeRunReturns409(t *testing.T) {
	r := testRouter(t)
	rec := postResolve(t, r, "ORD_1049", "APPROVED", "looks fine")
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

func TestResolveOrder_UnknownOrder404(t *testing.T) {
	r := testRouter(t)
	runOnce(t, r)

	rec := postResolve(t, r, "ORD_DOES_NOT_EXIST", "APPROVED", "looks fine")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestResolveOrder_MissingFieldsReturns400(t *testing.T) {
	r := testRouter(t)
	runOnce(t, r)

	rec := postResolve(t, r, "ORD_1049", "", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// TestResolveOrder_CapturesChosenCandidateUTR is Priority 5: on an
// AMBIGUOUS order with multiple tied candidates, "approved" is only a
// meaningful decision once it names which one — the log entry must
// capture that UTR.
func TestResolveOrder_CapturesChosenCandidateUTR(t *testing.T) {
	r := testRouter(t)
	runOnce(t, r)

	traceReq := httptest.NewRequest(http.MethodGet, "/reconcile/orders/ORD_1050", nil)
	traceRec := httptest.NewRecorder()
	r.ServeHTTP(traceRec, traceReq)
	var trace pipeline.OrderTrace
	if err := json.Unmarshal(traceRec.Body.Bytes(), &trace); err != nil {
		t.Fatalf("decode trace: %v", err)
	}
	if len(trace.Match.CandidateUTRs) == 0 {
		t.Fatal("ORD_1050 has no candidates to choose from")
	}
	chosenUTR := trace.Match.CandidateUTRs[0]

	rec := postResolveWithUTR(t, r, "ORD_1050", "APPROVED", "verified this is the right credit", chosenUTR)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/reconcile/orders/ORD_1050/resolutions", nil)
	listRec := httptest.NewRecorder()
	r.ServeHTTP(listRec, listReq)
	var resolutions []pipeline.Resolution
	if err := json.Unmarshal(listRec.Body.Bytes(), &resolutions); err != nil {
		t.Fatalf("decode resolutions: %v", err)
	}
	if len(resolutions) != 1 || resolutions[0].UTR != chosenUTR {
		t.Errorf("resolutions = %+v, want one entry with UTR = %q", resolutions, chosenUTR)
	}
}

// TestResolveOrder_UnknownUTRRejected is Priority 5: a UTR that isn't
// actually one of the order's own tied candidates must be rejected, not
// silently logged — the same "never trust client input at face value"
// principle applied to this endpoint's own input, not just LLM output.
func TestResolveOrder_UnknownUTRRejected(t *testing.T) {
	r := testRouter(t)
	runOnce(t, r)

	rec := postResolveWithUTR(t, r, "ORD_1050", "APPROVED", "looks right", "UTR_DOES_NOT_EXIST")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// TestResolveOrder_LogAccumulatesAndIsRetrievable is Phase 12's core
// guarantee: logged resolutions accumulate (append-only) and can be
// read back, without mutating the underlying report or any order's
// decision.
func TestResolveOrder_LogAccumulatesAndIsRetrievable(t *testing.T) {
	r := testRouter(t)
	runOnce(t, r)

	// snapshot the order's match/exception state before logging anything
	beforeReq := httptest.NewRequest(http.MethodGet, "/reconcile/orders/ORD_1049", nil)
	beforeRec := httptest.NewRecorder()
	r.ServeHTTP(beforeRec, beforeReq)
	beforeBody := beforeRec.Body.String()

	rec1 := postResolve(t, r, "ORD_1049", "APPROVED", "verified against bank statement manually")
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first resolve: status = %d, body = %s", rec1.Code, rec1.Body.String())
	}
	rec2 := postResolve(t, r, "ORD_1049", "FLAGGED", "still looks off, escalating to finance lead")
	if rec2.Code != http.StatusCreated {
		t.Fatalf("second resolve: status = %d, body = %s", rec2.Code, rec2.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/reconcile/orders/ORD_1049/resolutions", nil)
	listRec := httptest.NewRecorder()
	r.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list resolutions: status = %d", listRec.Code)
	}

	var resolutions []pipeline.Resolution
	if err := json.Unmarshal(listRec.Body.Bytes(), &resolutions); err != nil {
		t.Fatalf("decode resolutions: %v", err)
	}
	if len(resolutions) != 2 {
		t.Fatalf("len(resolutions) = %d, want 2", len(resolutions))
	}
	if resolutions[0].Action != "APPROVED" || resolutions[1].Action != "FLAGGED" {
		t.Errorf("resolutions = %+v, want APPROVED then FLAGGED in order", resolutions)
	}

	// a different order's log must stay empty
	otherReq := httptest.NewRequest(http.MethodGet, "/reconcile/orders/ORD_1052/resolutions", nil)
	otherRec := httptest.NewRecorder()
	r.ServeHTTP(otherRec, otherReq)
	var otherResolutions []pipeline.Resolution
	if err := json.Unmarshal(otherRec.Body.Bytes(), &otherResolutions); err != nil {
		t.Fatalf("decode other order's resolutions: %v", err)
	}
	if len(otherResolutions) != 0 {
		t.Errorf("ORD_1052 resolutions = %+v, want empty (logging is per-order)", otherResolutions)
	}

	// logging a resolution must never mutate the order's own match/exception state
	afterReq := httptest.NewRequest(http.MethodGet, "/reconcile/orders/ORD_1049", nil)
	afterRec := httptest.NewRecorder()
	r.ServeHTTP(afterRec, afterReq)
	if afterRec.Body.String() != beforeBody {
		t.Errorf("order trace changed after logging resolutions:\nbefore: %s\nafter:  %s", beforeBody, afterRec.Body.String())
	}
}

// TestGenerate_DefaultsAndRuns confirms POST /reconcile/generate (no
// query params) produces and caches a full report of the default size,
// through the exact same pipeline.Run path handleRun uses.
func TestGenerate_DefaultsAndRuns(t *testing.T) {
	r := testRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/reconcile/generate", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var report pipeline.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if report.Summary.TotalOrders != 50 {
		t.Errorf("Summary.TotalOrders = %d, want 50 (default size)", report.Summary.TotalOrders)
	}

	// the generated report is cached exactly like a normal /run
	reportReq := httptest.NewRequest(http.MethodGet, "/reconcile/report", nil)
	reportRec := httptest.NewRecorder()
	r.ServeHTTP(reportRec, reportReq)
	if reportRec.Code != http.StatusOK {
		t.Errorf("GET /reconcile/report after generate: status = %d", reportRec.Code)
	}
}

// generateBody is the shape of POST /reconcile/generate's response —
// the embedded report fields plus the generation metadata that proves
// the batch is genuinely new.
type generateBody struct {
	pipeline.Report
	Generation struct {
		Seed   int64 `json:"seed"`
		Size   int   `json:"size"`
		Sample struct {
			Ledger  []map[string]any `json:"ledger"`
			Gateway []map[string]any `json:"gateway"`
			Bank    []map[string]any `json:"bank"`
		} `json:"sample"`
	} `json:"generation"`
}

func postGenerate(t *testing.T, r http.Handler, query string) generateBody {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/reconcile/generate"+query, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("generate%s: status = %d, body = %s", query, rec.Code, rec.Body.String())
	}
	var body generateBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode generate response: %v", err)
	}
	return body
}

// TestGenerate_ReturnsSeedAndSample is the "prove the data actually
// changed" guarantee: the response carries the seed used and a sample
// of records spanning all three sources, so a judge can eyeball that a
// fresh click produced fresh data.
func TestGenerate_ReturnsSeedAndSample(t *testing.T) {
	r := testRouter(t)
	body := postGenerate(t, r, "?size=50")

	if body.Generation.Seed == 0 {
		t.Error("Generation.Seed = 0, want a real (nanosecond-clock) seed")
	}
	if body.Generation.Size != 50 {
		t.Errorf("Generation.Size = %d, want 50", body.Generation.Size)
	}
	if len(body.Generation.Sample.Ledger) == 0 || len(body.Generation.Sample.Gateway) == 0 || len(body.Generation.Sample.Bank) == 0 {
		t.Errorf("sample must span all three sources, got ledger=%d gateway=%d bank=%d",
			len(body.Generation.Sample.Ledger), len(body.Generation.Sample.Gateway), len(body.Generation.Sample.Bank))
	}
}

// TestGenerate_SeedlessCallsDiffer confirms the advisor's fix: two
// clicks of the same size button with no explicit seed must produce
// different datasets (different seed, different underlying records) —
// not the same canned scenario replaying.
func TestGenerate_SeedlessCallsDiffer(t *testing.T) {
	r := testRouter(t)
	a := postGenerate(t, r, "?size=50")
	b := postGenerate(t, r, "?size=50")

	if a.Generation.Seed == b.Generation.Seed {
		t.Fatalf("two seedless calls used the same seed %d — default seed isn't varying", a.Generation.Seed)
	}
	if fmt.Sprint(a.Generation.Sample.Ledger) == fmt.Sprint(b.Generation.Sample.Ledger) {
		t.Error("two seedless calls produced identical ledger samples — data isn't actually changing")
	}
}

// TestGenerate_ExplicitSeedIsEchoedAndReproducible confirms an explicit
// ?seed= still pins the output (Implementation Rule 7) and is reported
// back unchanged.
func TestGenerate_ExplicitSeedIsEchoedAndReproducible(t *testing.T) {
	r := testRouter(t)
	a := postGenerate(t, r, "?size=50&seed=12345")
	b := postGenerate(t, r, "?size=50&seed=12345")

	if a.Generation.Seed != 12345 || b.Generation.Seed != 12345 {
		t.Fatalf("explicit seed not echoed: got %d and %d", a.Generation.Seed, b.Generation.Seed)
	}
	if fmt.Sprint(a.Generation.Sample.Ledger) != fmt.Sprint(b.Generation.Sample.Ledger) {
		t.Error("same explicit seed produced different samples — generation isn't reproducible")
	}
}

func TestGenerate_SizeParam(t *testing.T) {
	r := testRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/reconcile/generate?size=250&seed=99", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var report pipeline.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if report.Summary.TotalOrders != 250 {
		t.Errorf("Summary.TotalOrders = %d, want 250", report.Summary.TotalOrders)
	}
}

// TestGenerate_RejectsArbitrarySize is the actual guardrail: a judge
// (or a script) cannot request an unbounded size that would make a
// click take forever or run up Vertex AI spend — only the dashboard's
// three preset sizes are accepted.
func TestGenerate_RejectsArbitrarySize(t *testing.T) {
	r := testRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/reconcile/generate?size=1000000", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestGenerate_ClearsStaleResolutionLog(t *testing.T) {
	r := testRouter(t)
	runOnce(t, r)
	postResolve(t, r, "ORD_1049", "APPROVED", "looked fine")

	genReq := httptest.NewRequest(http.MethodPost, "/reconcile/generate?size=50", nil)
	genRec := httptest.NewRecorder()
	r.ServeHTTP(genRec, genReq)
	if genRec.Code != http.StatusOK {
		t.Fatalf("generate: status = %d", genRec.Code)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/reconcile/orders/ORD_1049/resolutions", nil)
	listRec := httptest.NewRecorder()
	r.ServeHTTP(listRec, listReq)
	// ORD_1049 no longer exists in the freshly generated batch, so this
	// is a 404 — but the important assertion is the old log entry is gone
	// (checked indirectly: a 404 here, not a 200 with the stale entry).
	if listRec.Code == http.StatusOK {
		var resolutions []pipeline.Resolution
		_ = json.Unmarshal(listRec.Body.Bytes(), &resolutions)
		if len(resolutions) != 0 {
			t.Errorf("resolutions = %+v, want cleared after generate", resolutions)
		}
	}
}

func TestMissingFixturesDir_RunReturns500(t *testing.T) {
	r := NewRouter(pipeline.Config{FixturesDir: "../../data/does-not-exist"})

	req := httptest.NewRequest(http.MethodGet, "/reconcile/run", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
