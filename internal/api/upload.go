package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"razorpay_finance_controller/internal/pipeline"
)

// maxUploadFileSize bounds each uploaded file — a few hundred KB is
// generous for this data shape (a few hundred JSON records), and
// bounding it keeps a public write path from being used to push
// arbitrarily large payloads at the server (CLAUDE.md Phase 16 Tier 2).
const maxUploadFileSize = 500 * 1024

// uploadLimiter guards the one endpoint here with a real abuse surface:
// unlike /reconcile/generate (fixed, small, server-controlled sizes),
// an upload accepts arbitrary judge-submitted content and can be hit in
// a loop to run up real Vertex AI spend on the ambiguous slice of
// whatever they submit. 5 uploads per address, refilling one every 30s.
var uploadLimiter = newRateLimiter(5, 1.0/30.0)

// uploadFileSpec is one of the three-or-four files an upload accepts,
// each mapped onto the exact filename internal/ingest.LoadAll expects.
type uploadFileSpec struct {
	field    string
	filename string
	required bool
}

var uploadFiles = []uploadFileSpec{
	{"ledger", "ledger.json", true},
	{"gateway", "gateway.json", true},
	{"bank", "bank.json", true},
	{"ground_truth", "ground_truth.json", false},
}

// handleUpload accepts a judge-submitted ledger/gateway/bank dataset
// (multipart form, one file per field) and runs the exact same
// pipeline.Run used everywhere else against it — the same unmodified
// internal/ingest parsers and validators are what protect this endpoint,
// not anything upload-specific. A malformed or invalid submission comes
// back as a 400 with the real validation error text (e.g. "gross_amount
// must be positive"), not a 500: this is the user's input, not a server
// fault, and CLAUDE.md's ingest validation already turns exactly this
// case into a clear, row-level message instead of a crash.
func (s *Server) handleUpload(c *gin.Context) {
	if !uploadLimiter.Allow(c.ClientIP()) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many uploads from this address — please wait a bit and try again"})
		return
	}

	dir, err := os.MkdirTemp("", "afc-uploaded-*")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer os.RemoveAll(dir)

	for _, spec := range uploadFiles {
		fileHeader, ferr := c.FormFile(spec.field)
		if ferr != nil {
			if spec.required {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("missing required file field %q", spec.field)})
				return
			}
			continue
		}
		if fileHeader.Size > maxUploadFileSize {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("%s exceeds the %dKB size limit", spec.field, maxUploadFileSize/1024)})
			return
		}
		if err := c.SaveUploadedFile(fileHeader, filepath.Join(dir, spec.filename)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	cfg := s.cfg
	cfg.FixturesDir = dir
	report, err := pipeline.Run(c.Request.Context(), cfg)
	if err != nil {
		// A pipeline failure here is almost always the submitted data
		// itself (malformed JSON, a negative amount, a missing field) —
		// the caller's mistake, not this server's, so it's a 400 with
		// the real ingest error text surfaced verbatim.
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	s.mu.Lock()
	s.last = &report
	// An uploaded dataset has its own, unrelated order IDs — clear the
	// resolution log so a stale entry from a previous dataset can't
	// misleadingly appear to apply to this one (same reasoning as
	// handleGenerate).
	s.resolutions = nil
	s.mu.Unlock()

	c.JSON(http.StatusOK, report)
}
