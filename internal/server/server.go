// Package server implements the go-search HTTP API: routes, handlers and
// request-hardening middleware.
package server

import (
	"log"
	"net/http"

	"search-engine/internal/config"
	"search-engine/internal/crawljob"
	"search-engine/internal/index"
	"search-engine/internal/ratelimit"
)

// Request body limits.
const (
	maxCrawlBody = 16 << 10 // 16 KiB
	maxEvalBody  = 1 << 20  // 1 MiB
)

// Concurrency limits for crawl jobs.
const (
	maxRunningCrawls = 2   // crawls allowed at once
	maxJobHistory    = 100 // finished jobs kept for status queries
)

// Server bundles the shared state the HTTP handlers need.
type Server struct {
	cfg  config.Config
	idx  *index.Index
	jobs *crawljob.Manager
	ql   *index.QueryLog

	// Per-client rate limits.
	publicLimit *ratelimit.Limiter // search, index stats
	adminLimit  *ratelimit.Limiter // admin routes, checked before the key
	crawlLimit  *ratelimit.Limiter // charged only when a crawl actually starts
}

// New creates a Server serving idx.
func New(cfg config.Config, idx *index.Index) *Server {
	return &Server{
		cfg:         cfg,
		idx:         idx,
		jobs:        crawljob.NewManager(maxRunningCrawls, maxJobHistory),
		ql:          index.NewQueryLog(),
		publicLimit: ratelimit.New("search", 120, 30),   // 2 req/s, burst 30
		adminLimit:  ratelimit.New("admin", 30, 10),     // 0.5 req/s, burst 10
		crawlLimit:  ratelimit.New("crawl", 10.0/60, 3), // 10 per hour, burst 3
	}
}

// Handler returns the HTTP handler with all routes and middleware.
func (s *Server) Handler() http.Handler {
	// Admin routes are rate limited before the key check, so the key cannot
	// be brute-forced quickly.
	admin := func(h http.HandlerFunc) http.HandlerFunc {
		return s.rateLimit(s.adminLimit, requireAdmin(s.cfg.AdminKey, h))
	}
	public := func(h http.HandlerFunc) http.HandlerFunc {
		return s.rateLimit(s.publicLimit, h)
	}

	mux := http.NewServeMux()

	// Public
	mux.HandleFunc("/health", method(http.MethodGet, s.handleHealth))
	mux.HandleFunc("/search", method(http.MethodGet, public(s.handleSearch)))
	mux.HandleFunc("/index/stats", method(http.MethodGet, public(s.handleIndexStats)))

	// Admin
	mux.HandleFunc("/crawl", method(http.MethodPost, admin(limitBody(maxCrawlBody, s.handleStartCrawl))))
	mux.HandleFunc("/crawl/status/", method(http.MethodGet, admin(s.handleCrawlStatus)))
	mux.HandleFunc("/crawl/jobs", method(http.MethodGet, admin(s.handleListJobs)))
	mux.HandleFunc("/search/stats", method(http.MethodGet, admin(s.handleSearchStats)))
	mux.HandleFunc("/eval", method(http.MethodPost, admin(limitBody(maxEvalBody, s.handleEval))))

	return logRequests(securityHeaders(mux))
}

// SaveIndex persists the index if it has unsaved documents. It runs after
// every crawl and on shutdown.
func (s *Server) SaveIndex() {
	if !s.idx.Dirty() {
		return
	}
	stats := s.idx.Stats()
	log.Printf("[INDEX] Saving index: %d terms, %d docs, %d postings",
		stats.TermCount, stats.DocCount, stats.TotalPostings)
	if err := index.SaveIndex(s.idx, s.cfg.IndexDir()); err != nil {
		log.Printf("[ERROR] Failed to save index: %v", err)
		return
	}
	log.Printf("[INDEX] Index saved to %s", s.cfg.IndexDir())
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "go-search"})
}

func (s *Server) handleIndexStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.idx.Stats())
}
