// Package api wraps the reconciliation pipeline in thin HTTP handlers.
// No business logic lives here — it belongs to internal/pipeline and
// the packages it orchestrates (CLAUDE.md Implementation Rule 3).
// See CLAUDE.md Phase 6.
package api

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"razorpay_finance_controller/internal/gen"
	"razorpay_finance_controller/internal/pipeline"
)

// Server holds the in-memory state for one running instance: the
// pipeline configuration, the most recently computed report, and an
// append-only log of human resolution decisions (Phase 12). In-memory
// is correct for this MVP's scale (CLAUDE.md tech stack) — no database,
// no persistence across restarts.
type Server struct {
	cfg         pipeline.Config
	mu          sync.RWMutex
	last        *pipeline.Report
	resolutions []pipeline.Resolution
}

// NewRouter builds the Gin engine and registers the reconciliation
// endpoints against a fresh Server.
func NewRouter(cfg pipeline.Config) *gin.Engine {
	s := &Server{cfg: cfg}

	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()
	r.GET("/healthz", handleHealthz)
	r.GET("/reconcile/run", s.handleRun)
	r.POST("/reconcile/generate", s.handleGenerate)
	r.POST("/reconcile/upload", s.handleUpload)
	r.GET("/reconcile/report", s.handleReport)
	r.GET("/reconcile/exceptions", s.handleExceptions)
	r.GET("/reconcile/orders/:id", s.handleOrderTrace)
	r.POST("/reconcile/orders/:id/resolve", s.handleResolveOrder)
	r.GET("/reconcile/orders/:id/resolutions", s.handleListResolutions)
	return r
}

// handleHealthz is Cloud Run's startup/liveness probe target (Phase 15).
// Deliberately independent of pipeline state: a fresh instance that
// hasn't run a reconciliation yet is still healthy — "no report cached
// yet" is not the same as "not ready to serve traffic".
func handleHealthz(c *gin.Context) {
	c.Status(http.StatusOK)
}

// handleRun executes the full pipeline live and caches the result. This
// is what the dashboard's "Run Reconciliation" button calls.
func (s *Server) handleRun(c *gin.Context) {
	report, err := pipeline.Run(c.Request.Context(), s.cfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	s.mu.Lock()
	s.last = &report
	s.mu.Unlock()

	c.JSON(http.StatusOK, report)
}

// allowedGenerateSizes is a fixed, small menu, not a free-form field —
// a public "generate" button is not a place to let arbitrary input
// make a demo click take forever or run up Vertex AI spend (CLAUDE.md
// Phase 16 Tier 1). Matches the dashboard's three size buttons exactly.
var allowedGenerateSizes = map[int]bool{50: true, 100: true, 250: true}

// generateResponse is handleGenerate's body: the full reconciliation
// Report (embedded, so every field the dashboard's shared render path
// already reads stays top-level) plus the generation metadata that
// proves the batch is genuinely new — the seed used and a small sample
// of the underlying data. A stable seed number shown next to visibly
// different sample records is checkable proof this isn't one canned
// scenario replaying.
type generateResponse struct {
	pipeline.Report
	Generation generationInfo `json:"generation"`
}

type generationInfo struct {
	Seed   int64      `json:"seed"`
	Size   int        `json:"size"`
	Sample gen.Sample `json:"sample"`
}

// handleGenerate builds a fresh, internally-consistent synthetic batch
// (internal/gen), writes it to a temporary directory, and runs the full
// pipeline against it exactly the way handleRun runs against the
// checked-in fixtures — same ingest parsers, same matching engine, same
// AI judge. Proves the system isn't replaying one canned scenario: a
// judge watches it reconcile data that didn't exist seconds earlier.
func (s *Server) handleGenerate(c *gin.Context) {
	size := 50
	if sizeParam := c.Query("size"); sizeParam != "" {
		parsed, err := strconv.Atoi(sizeParam)
		if err != nil || !allowedGenerateSizes[parsed] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "size must be one of 50, 100, 250"})
			return
		}
		size = parsed
	}

	// Default the seed to the current nanosecond clock, so two clicks of
	// the same size button produce genuinely different datasets — different
	// customers, amounts, dates, and escalated order IDs — not the same
	// canned scenario. An explicit ?seed= still pins the output for
	// reproducibility (Implementation Rule 7).
	seed := time.Now().UnixNano()
	if seedParam := c.Query("seed"); seedParam != "" {
		parsed, err := strconv.ParseInt(seedParam, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "seed must be an integer"})
			return
		}
		seed = parsed
	}

	dir, err := os.MkdirTemp("", "afc-generated-*")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer os.RemoveAll(dir) // pipeline.Run reads these synchronously; the Report is fully in-memory once it returns

	batch := gen.Generate(size, seed)
	if err := batch.WriteTo(dir); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	cfg := s.cfg
	cfg.FixturesDir = dir
	report, err := pipeline.Run(c.Request.Context(), cfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	s.mu.Lock()
	s.last = &report
	// A freshly generated batch reuses the same ORD_/PAY_/UTR numbering
	// scheme as any previous run — clear the resolution log so it can't
	// read as referring to this new dataset's (different) orders.
	s.resolutions = nil
	s.mu.Unlock()

	c.JSON(http.StatusOK, generateResponse{
		Report:     report,
		Generation: generationInfo{Seed: seed, Size: size, Sample: batch.Preview(3)},
	})
}

// handleReport returns the most recently computed report without
// re-running the pipeline.
func (s *Server) handleReport(c *gin.Context) {
	report := s.currentReport()
	if report == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "no reconciliation has been run yet; call GET /reconcile/run first"})
		return
	}
	c.JSON(http.StatusOK, report)
}

// handleExceptions returns just the exception list from the most
// recently computed report.
func (s *Server) handleExceptions(c *gin.Context) {
	report := s.currentReport()
	if report == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "no reconciliation has been run yet; call GET /reconcile/run first"})
		return
	}
	c.JSON(http.StatusOK, report.Exceptions)
}

// handleOrderTrace returns the combined match/verdict/exception view for
// one order from the most recently computed report — a read-only
// aggregation over what Run already computed, no new pipeline run.
func (s *Server) handleOrderTrace(c *gin.Context) {
	report := s.currentReport()
	if report == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "no reconciliation has been run yet; call GET /reconcile/run first"})
		return
	}

	orderID := c.Param("id")
	trace, found := report.Trace(orderID)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("order %q not found in the latest reconciliation run", orderID)})
		return
	}
	c.JSON(http.StatusOK, trace)
}

// resolveRequest is the body POST /reconcile/orders/:id/resolve
// accepts: a human's chosen action, their reason for it, and — for a
// candidate-specific action on an AMBIGUOUS order — which UTR they
// picked (Precision & Impressiveness Pass, Priority 5).
type resolveRequest struct {
	Action string `json:"action" binding:"required"`
	Reason string `json:"reason" binding:"required"`
	UTR    string `json:"utr"`
}

// handleResolveOrder logs a human's decision about one order into the
// append-only resolution log. It never mutates the cached Report or any
// order's match/exception state — this is a log entry, not an action;
// no money moves and no decision is overwritten (CLAUDE.md Phase 12,
// and the "no autonomous money movement" guardrail).
func (s *Server) handleResolveOrder(c *gin.Context) {
	report := s.currentReport()
	if report == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "no reconciliation has been run yet; call GET /reconcile/run first"})
		return
	}

	orderID := c.Param("id")
	trace, found := report.Trace(orderID)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("order %q not found in the latest reconciliation run", orderID)})
		return
	}

	var req resolveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// A named UTR must actually be one of this order's own tied
	// candidates — never trust client input at face value (CLAUDE.md
	// Implementation Rule 8's spirit applies here too, not just to LLM
	// output): a typo or a stale UI shouldn't silently log a decision
	// against a UTR this order was never even ambiguous about.
	if req.UTR != "" {
		valid := false
		for _, utr := range trace.Match.CandidateUTRs {
			if utr == req.UTR {
				valid = true
				break
			}
		}
		if !valid {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("utr %q is not one of this order's candidates", req.UTR)})
			return
		}
	}

	res := pipeline.Resolution{OrderID: orderID, Action: req.Action, UTR: req.UTR, Reason: req.Reason, CreatedAt: time.Now()}
	s.mu.Lock()
	s.resolutions = append(s.resolutions, res)
	s.mu.Unlock()

	c.JSON(http.StatusCreated, res)
}

// handleListResolutions returns the append-only resolution log for one
// order, oldest first — the retrievable side of Phase 12's feedback
// loop seed.
func (s *Server) handleListResolutions(c *gin.Context) {
	report := s.currentReport()
	if report == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "no reconciliation has been run yet; call GET /reconcile/run first"})
		return
	}

	orderID := c.Param("id")
	if _, found := report.Trace(orderID); !found {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("order %q not found in the latest reconciliation run", orderID)})
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]pipeline.Resolution, 0)
	for _, r := range s.resolutions {
		if r.OrderID == orderID {
			out = append(out, r)
		}
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) currentReport() *pipeline.Report {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.last
}
