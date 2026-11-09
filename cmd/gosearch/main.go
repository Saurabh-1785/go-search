// Command gosearch runs the go-search HTTP server.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"search-engine/internal/config"
	"search-engine/internal/index"
	"search-engine/internal/netguard"
	"search-engine/internal/server"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("[ERROR] %v", err)
	}
	if cfg.GeneratedKey {
		log.Printf("[WARN]  ADMIN_API_KEY not set — generated a key for this run only: %s", cfg.AdminKey)
	}

	if cfg.AllowPrivateTargets {
		log.Printf("[WARN]  ALLOW_PRIVATE_TARGETS=true — SSRF protection is OFF. Use only for local development.")
	} else {
		netguard.Install()
	}

	idx := loadIndex(cfg.IndexDir())
	srv := server.New(cfg, idx)

	httpSrv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second, // Slowloris protection
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}

	printBanner(httpSrv.Addr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.ListenAndServe() }()

	select {
	case err := <-serveErr:
		log.Fatalf("[ERROR] Server failed: %v", err)
	case <-ctx.Done():
	}

	// Graceful shutdown: stop taking requests, then save anything indexed
	// since the last save. Container platforms send SIGTERM before killing
	// the process (e.g. Azure scale-to-zero), so this is what keeps the
	// index across restarts when a crawl is still running.
	log.Printf("[INFO]  Shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("[WARN]  HTTP shutdown: %v", err)
	}
	srv.SaveIndex()
	log.Printf("[INFO]  Bye.")
}

// loadIndex restores the index saved by a previous run, or starts empty.
func loadIndex(dir string) *index.Index {
	idx, err := index.LoadIndex(dir)
	switch {
	case errors.Is(err, index.ErrNoIndex):
		log.Printf("[INFO]  No saved index in %s — starting empty", dir)
		return index.NewIndex()
	case err != nil:
		// Refuse to start rather than run empty and overwrite good data
		// with the next save.
		log.Fatalf("[ERROR] Failed to load index from %s: %v", dir, err)
	}
	s := idx.Stats()
	log.Printf("[INFO]  Loaded index from %s: %d docs, %d terms", dir, s.DocCount, s.TermCount)
	return idx
}

func printBanner(addr string) {
	fmt.Println()
	fmt.Println("  ╔═══════════════════════════════════╗")
	fmt.Println("  ║         GoSearch API v1.0          ║")
	fmt.Println("  ╚═══════════════════════════════════╝")
	fmt.Println()
	log.Printf("[INFO]  Server starting on %s", addr)
	log.Printf("[INFO]  Public endpoints:")
	log.Printf("[INFO]    GET  /search?q=...&k=10   — BM25 ranked search")
	log.Printf("[INFO]    GET  /index/stats         — index statistics")
	log.Printf("[INFO]    GET  /health              — health check")
	log.Printf("[INFO]  Admin endpoints (Authorization: Bearer <ADMIN_API_KEY>):")
	log.Printf("[INFO]    POST /crawl              — start a crawl job")
	log.Printf("[INFO]    GET  /crawl/status/:id   — check job status")
	log.Printf("[INFO]    GET  /crawl/jobs          — list all jobs")
	log.Printf("[INFO]    GET  /search/stats        — query latency & QPS")
	log.Printf("[INFO]    POST /eval               — precision@K & recall")
}
