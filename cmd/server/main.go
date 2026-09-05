// Command server wires the reconciliation pipeline together, embeds
// the dashboard, and starts the HTTP server. See CLAUDE.md.
package main

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"razorpay_finance_controller/internal/api"
	"razorpay_finance_controller/internal/envfile"
	"razorpay_finance_controller/internal/judge"
	"razorpay_finance_controller/internal/pipeline"
	"razorpay_finance_controller/web"

	"github.com/gin-gonic/gin"
)

func main() {
	ctx := context.Background()

	if err := envfile.Load(".env"); err != nil {
		log.Printf("warning: failed to load .env: %v", err)
	}

	cfg := pipeline.Config{FixturesDir: "data/fixtures"}
	if provider, err := judge.NewGeminiProviderFromEnv(ctx); err != nil {
		log.Printf("AI judge disabled (%v) — ambiguous orders will be reported as pending", err)
	} else {
		cfg.Judge = provider
	}

	r := api.NewRouter(cfg)

	static, err := fs.Sub(web.StaticFS, "static")
	if err != nil {
		log.Fatal(err)
	}
	fileServer := http.FileServer(http.FS(static))
	r.NoRoute(gin.WrapH(fileServer))

	// Cloud Run sets $PORT and expects the container to listen on it
	// (Phase 15); :8080 remains the local-dev default when unset.
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	srv := &http.Server{Addr: ":" + port, Handler: r}

	// Cloud Run sends SIGTERM on scale-down or redeploy, then waits a
	// grace period before killing the process outright. Shutdown lets
	// in-flight requests finish instead of dropping them mid-response,
	// rather than exiting immediately on signal receipt.
	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		log.Println("listening on :" + port)
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	case <-sigCtx.Done():
		log.Println("shutting down: waiting for in-flight requests to finish")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("graceful shutdown failed: %v", err)
		}
	}
}
