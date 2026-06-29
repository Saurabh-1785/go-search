package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"search-engine/index"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Initialize job manager
	jm := NewJobManager()

	// Initialize inverted index (shared across all crawl jobs)
	idx := index.NewIndex()

	// Initialize query log for latency/QPS tracking
	ql := index.NewQueryLog()

	// Set up routes
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"service": "go-search",
		})
	})

	// Index stats — returns term count, doc count, total postings
	mux.HandleFunc("/index/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, idx.Stats())
	})

	// Crawl API
	registerCrawlRoutes(mux, jm, idx)

	// Search API
	registerSearchRoutes(mux, idx, ql)

	// Start server
	addr := ":" + port
	fmt.Println()
	fmt.Println("  ╔═══════════════════════════════════╗")
	fmt.Println("  ║         GoSearch API v3.0          ║")
	fmt.Println("  ╚═══════════════════════════════════╝")
	fmt.Println()
	log.Printf("[INFO]  Server starting on %s", addr)
	log.Printf("[INFO]  Endpoints:")
	log.Printf("[INFO]    POST /crawl              — start a crawl job")
	log.Printf("[INFO]    GET  /crawl/status/:id   — check job status")
	log.Printf("[INFO]    GET  /crawl/jobs          — list all jobs")
	log.Printf("[INFO]    GET  /search?q=...&k=10   — BM25 ranked search")
	log.Printf("[INFO]    GET  /search/stats        — query latency & QPS")
	log.Printf("[INFO]    POST /eval               — precision@K & recall")
	log.Printf("[INFO]    GET  /index/stats         — index statistics")
	log.Printf("[INFO]    GET  /health              — health check")

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("[ERROR] Server failed: %v", err)
	}
}
