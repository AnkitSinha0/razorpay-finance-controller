package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"razorpay_finance_controller/internal/pipeline"
)

// buildUpload constructs a multipart/form-data request body from
// field -> raw file content, using the exact form field names
// handleUpload expects.
func buildUpload(t *testing.T, files map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for field, content := range files {
		part, err := w.CreateFormFile(field, field+".json")
		if err != nil {
			t.Fatalf("CreateFormFile(%s): %v", field, err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", field, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &buf, w.FormDataContentType()
}

func postUpload(t *testing.T, r http.Handler, files map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := buildUpload(t, files)
	req := httptest.NewRequest(http.MethodPost, "/reconcile/upload", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

const validLedger = `[{"order_id":"ORD_1","customer":"Amit","gross_amount":1000000,"status":"PAID"}]`
const validGateway = `[{"payment_id":"PAY_1","order_id":"ORD_1","gross_amount":1000000,"fee":18000,"tax":3240,"net_amount":978760,"settlement_id":"ST_1","settlement_date":"2026-08-01"}]`
const validBank = `[{"utr":"UTR000001","date":"2026-08-01","credit_amount":978760,"description":"RAZORPAY PAY_1"}]`

func TestUpload_ValidDatasetRunsAndCaches(t *testing.T) {
	r := testRouter(t)
	rec := postUpload(t, r, map[string]string{
		"ledger":  validLedger,
		"gateway": validGateway,
		"bank":    validBank,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var report pipeline.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if report.Summary.TotalOrders != 1 {
		t.Errorf("TotalOrders = %d, want 1", report.Summary.TotalOrders)
	}
	// no ground truth was submitted — metrics.Compute must omit the
	// accuracy figure honestly (nil), not report a scary 0%.
	if report.Summary.GroundTruthAccuracy != nil {
		t.Errorf("GroundTruthAccuracy = %v, want nil (no ground truth submitted)", *report.Summary.GroundTruthAccuracy)
	}

	// cached exactly like any other run
	reportReq := httptest.NewRequest(http.MethodGet, "/reconcile/report", nil)
	reportRec := httptest.NewRecorder()
	r.ServeHTTP(reportRec, reportReq)
	if reportRec.Code != http.StatusOK {
		t.Errorf("GET /reconcile/report after upload: status = %d", reportRec.Code)
	}
}

func TestUpload_MissingRequiredFileReturns400(t *testing.T) {
	r := testRouter(t)
	rec := postUpload(t, r, map[string]string{
		"ledger":  validLedger,
		"gateway": validGateway,
		// bank omitted
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "bank") {
		t.Errorf("body = %s, want it to name the missing field", rec.Body.String())
	}
}

// TestUpload_MalformedDataReturnsSameValidationErrorAsIngest is Tier
// 2's own test spec: a deliberately malformed upload (negative amount)
// must surface through the exact same internal/ingest validation path
// every fixture-based run already uses — confirming upload didn't fork
// it — and come back as a 400 (the submitter's mistake), not a 500.
func TestUpload_MalformedDataReturnsSameValidationErrorAsIngest(t *testing.T) {
	r := testRouter(t)
	badLedger := `[{"order_id":"ORD_1","customer":"Amit","gross_amount":-500,"status":"PAID"}]`
	rec := postUpload(t, r, map[string]string{
		"ledger":  badLedger,
		"gateway": validGateway,
		"bank":    validBank,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	wantSubstring := "gross_amount must be positive, got -500"
	if !strings.Contains(rec.Body.String(), wantSubstring) {
		t.Errorf("body = %s, want it to contain %q (the same text internal/ingest's own tests expect)", rec.Body.String(), wantSubstring)
	}
}

func TestUpload_OversizedFileRejected(t *testing.T) {
	r := testRouter(t)
	huge := "[" + strings.Repeat(`{"order_id":"ORD_1","customer":"Amit","gross_amount":100,"status":"PAID"},`, 30000) + "]"
	if len(huge) <= maxUploadFileSize {
		t.Fatalf("test fixture isn't actually oversized: %d bytes", len(huge))
	}
	rec := postUpload(t, r, map[string]string{
		"ledger":  huge,
		"gateway": validGateway,
		"bank":    validBank,
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestUpload_RateLimited(t *testing.T) {
	r := testRouter(t)
	// drain this test's own budget without touching the shared limiter's
	// state from other tests: use a fresh limiter directly.
	orig := uploadLimiter
	uploadLimiter = newRateLimiter(2, 0)
	defer func() { uploadLimiter = orig }()

	files := map[string]string{"ledger": validLedger, "gateway": validGateway, "bank": validBank}
	for i := 0; i < 2; i++ {
		rec := postUpload(t, r, files)
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d: status = %d, want 200", i+1, rec.Code)
		}
	}
	rec := postUpload(t, r, files)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("3rd call: status = %d, want 429", rec.Code)
	}
}

func TestUpload_ClearsStaleResolutionLog(t *testing.T) {
	r := testRouter(t)
	runOnce(t, r)
	postResolve(t, r, "ORD_1049", "APPROVED", "looked fine")

	rec := postUpload(t, r, map[string]string{
		"ledger":  validLedger,
		"gateway": validGateway,
		"bank":    validBank,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/reconcile/orders/ORD_1049/resolutions", nil)
	listRec := httptest.NewRecorder()
	r.ServeHTTP(listRec, listReq)
	if listRec.Code == http.StatusOK {
		var resolutions []pipeline.Resolution
		_ = json.Unmarshal(listRec.Body.Bytes(), &resolutions)
		if len(resolutions) != 0 {
			t.Errorf("resolutions = %+v, want cleared after upload", resolutions)
		}
	}
}

func init() {
	// sanity check the test fixtures above are internally consistent
	// (net_amount really does equal gross-fee-tax) so a future edit to
	// them can't silently start failing ingest's own reconciliation.
	fee, tax := int64(18000), int64(3240)
	gross, net := int64(1000000), int64(978760)
	if gross-fee-tax != net {
		panic(fmt.Sprintf("test fixture inconsistency: %d - %d - %d != %d", gross, fee, tax, net))
	}
}
