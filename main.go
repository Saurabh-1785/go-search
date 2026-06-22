package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Initialize job manager
	jm := NewJobManager()

	// Set up routes
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status": "ok",
			"service": "go-search",
		})
	})

	// Crawl API
	registerCrawlRoutes(mux, jm)

	// Start server
	addr := ":" + port
	fmt.Println()
	fmt.Println("  ╔═══════════════════════════════════╗")
	fmt.Println("  ║         GoSearch API v1.0          ║")
	fmt.Println("  ╚═══════════════════════════════════╝")
	fmt.Println()
	log.Printf("[INFO]  Server starting on %s", addr)
	log.Printf("[INFO]  Endpoints:")
	log.Printf("[INFO]    POST /crawl              — start a crawl job")
	log.Printf("[INFO]    GET  /crawl/status/:id   — check job status")
	log.Printf("[INFO]    GET  /crawl/jobs          — list all jobs")
	log.Printf("[INFO]    GET  /health              — health check")

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("[ERROR] Server failed: %v", err)
	}
}
