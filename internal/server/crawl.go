package server

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"search-engine/internal/crawljob"
	"search-engine/internal/netguard"

	"github.com/Saurabh-1785/gocrawl/extractor"
)

// handleStartCrawl handles POST /crawl (admin only).
//
// Request body:
//
//	{
//	  "url": "https://example.com",
//	  "depth": 2,        // 1-3, default 2
//	  "max_pages": 100,  // 1-500, default 50
//	  "workers": 5       // 1-8, default 3
//	}
//
// Response (202):
//
//	{ "job_id": "abc123def456", "status": "running" }
func (s *Server) handleStartCrawl(w http.ResponseWriter, r *http.Request) {
	var req crawljob.Request
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	u, err := netguard.ValidateURL(r.Context(), req.URL, s.cfg.AllowPrivateTargets)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.URL = u.String()
	if err := req.Normalize(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Crawl quota is spent only by valid requests, so a typo doesn't burn it.
	if !s.allow(w, r, s.crawlLimit) {
		return
	}

	// Feeds each crawled document into the inverted index. Called serially
	// by the job's single consumer goroutine (see crawljob).
	onDocument := func(doc extractor.Document) {
		s.idx.AddDocument(doc.ID, doc.URL, doc.Title, doc.Content)
		log.Printf("[INDEX] doc %s — %d bytes", shortID(doc.ID), doc.ContentLength)
	}

	jobID, err := s.jobs.Start(req, onDocument, s.SaveIndex)
	if errors.Is(err, crawljob.ErrTooManyJobs) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "too many crawl jobs running, try again later")
		return
	}

	log.Printf("[CRAWL] job %s started: depth=%d max_pages=%d workers=%d", jobID, req.Depth, req.MaxPages, req.Workers)
	writeJSON(w, http.StatusAccepted, map[string]string{
		"job_id": jobID,
		"status": string(crawljob.StatusRunning),
	})
}

// handleCrawlStatus handles GET /crawl/status/{id} (admin only).
func (s *Server) handleCrawlStatus(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimPrefix(r.URL.Path, "/crawl/status/")
	if jobID == "" {
		writeError(w, http.StatusBadRequest, "job_id is required in URL path")
		return
	}
	job := s.jobs.Get(jobID)
	if job == nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// handleListJobs handles GET /crawl/jobs (admin only), newest first.
func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	jobs := s.jobs.List()
	writeJSON(w, http.StatusOK, map[string]any{
		"jobs":  jobs,
		"count": len(jobs),
	})
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
