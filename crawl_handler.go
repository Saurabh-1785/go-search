package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"search-engine/index"

	"github.com/Saurabh-1785/gocrawl/extractor"
)

// registerCrawlRoutes sets up the crawl API endpoints on the given mux.
func registerCrawlRoutes(mux *http.ServeMux, jm *JobManager, idx *index.Index) {
	mux.HandleFunc("/crawl", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		handleStartCrawl(w, r, jm, idx)
	})

	mux.HandleFunc("/crawl/status/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		handleCrawlStatus(w, r, jm)
	})

	mux.HandleFunc("/crawl/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		handleListJobs(w, r, jm)
	})
}

// handleStartCrawl handles POST /crawl
//
// Request body:
//
//	{
//	  "url": "https://example.com",
//	  "depth": 2,
//	  "max_pages": 100,
//	  "workers": 5,
//	  "output_path": "data/corpus.jsonl"   // optional
//	}
//
// Response:
//
//	{
//	  "job_id": "abc123def456",
//	  "status": "running"
//	}
func handleStartCrawl(w http.ResponseWriter, r *http.Request, jm *JobManager, idx *index.Index) {
	var req CrawlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid JSON body: " + err.Error(),
		})
		return
	}

	if req.URL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "url is required",
		})
		return
	}

	// OnDocument callback — feeds each crawled document into the inverted index.
	// The index's AddDocument is thread-safe, but the crawl pipeline already
	// serializes calls through a single consumer goroutine (see crawl_job.go).
	onDocument := func(doc extractor.Document) {
		idx.AddDocument(doc.ID, doc.URL, doc.Title, doc.Content)
		log.Printf("[INDEX] doc #%d: %s — %s (%d bytes)",
			idx.Stats().DocCount, doc.ID[:12], doc.Title, doc.ContentLength)
	}

	// Save the index to disk when the crawl finishes.
	onComplete := func() {
		stats := idx.Stats()
		log.Printf("[INDEX] Crawl complete. Saving index: %d terms, %d docs, %d postings",
			stats.TermCount, stats.DocCount, stats.TotalPostings)
		if err := index.SaveIndex(idx, "data/index"); err != nil {
			log.Printf("[ERROR] Failed to save index: %v", err)
		} else {
			log.Printf("[INDEX] Index saved to data/index/")
		}
	}

	jobID := jm.StartJob(req, onDocument, onComplete)

	writeJSON(w, http.StatusAccepted, map[string]string{
		"job_id": jobID,
		"status": string(JobStatusRunning),
	})
}

// handleCrawlStatus handles GET /crawl/status/:id
//
// Response:
//
//	{
//	  "job_id": "abc123def456",
//	  "status": "completed",
//	  "seed_url": "https://example.com",
//	  "result": {
//	    "documents_written": 42,
//	    "pages_visited": 50,
//	    "pages_failed": 3,
//	    "duration": "12.345s"
//	  }
//	}
func handleCrawlStatus(w http.ResponseWriter, r *http.Request, jm *JobManager) {
	// Extract job ID from URL path: /crawl/status/{id}
	jobID := strings.TrimPrefix(r.URL.Path, "/crawl/status/")
	if jobID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "job_id is required in URL path",
		})
		return
	}

	job := jm.GetJob(jobID)
	if job == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "job not found",
		})
		return
	}

	writeJSON(w, http.StatusOK, job)
}

// handleListJobs handles GET /crawl/jobs
// Returns all jobs with their current status.
func handleListJobs(w http.ResponseWriter, r *http.Request, jm *JobManager) {
	jobs := jm.ListJobs()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"jobs":  jobs,
		"count": len(jobs),
	})
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
