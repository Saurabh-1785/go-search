package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/Saurabh-1785/gocrawl/extractor"
)

// registerCrawlRoutes sets up the crawl API endpoints on the given mux.
func registerCrawlRoutes(mux *http.ServeMux, jm *JobManager) {
	mux.HandleFunc("/crawl", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		handleStartCrawl(w, r, jm)
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
func handleStartCrawl(w http.ResponseWriter, r *http.Request, jm *JobManager) {
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

	// OnDocument callback — for now, just log.
	// In the future, this is where you'd plug in your indexer:
	//   indexer.Add(doc)
	onDocument := func(doc extractor.Document) {
		log.Printf("[CRAWL] Indexed document: %s — %s (%d bytes)",
			doc.ID[:12], doc.Title, doc.ContentLength)
	}

	jobID := jm.StartJob(req, onDocument)

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
