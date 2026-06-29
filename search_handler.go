package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"search-engine/index"
)

// registerSearchRoutes sets up the search API endpoints on the given mux.
func registerSearchRoutes(mux *http.ServeMux, idx *index.Index, ql *index.QueryLog) {
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		handleSearch(w, r, idx, ql)
	})

	mux.HandleFunc("/search/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, ql.Stats())
	})

	mux.HandleFunc("/eval", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		handleEval(w, r, idx)
	})
}

// handleSearch handles GET /search
//
// Query parameters:
//
//	q     — search query (required)
//	k     — max results (default 10)
//	mode  — "or" (default) or "and"
//
// Response:
//
//	{
//	  "query": "go programming",
//	  "results": [
//	    {
//	      "doc_id": 0,
//	      "url": "https://go.dev",
//	      "title": "The Go Programming Language",
//	      "score": 2.345,
//	      "snippet": "...The <mark>Go</mark> <mark>Programming</mark> Language..."
//	    }
//	  ],
//	  "total_hits": 15,
//	  "time_taken_ms": 1.23
//	}
func handleSearch(w http.ResponseWriter, r *http.Request, idx *index.Index, ql *index.QueryLog) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "query parameter 'q' is required",
		})
		return
	}

	// Parse optional parameters.
	topK := 10
	if kStr := r.URL.Query().Get("k"); kStr != "" {
		if k, err := strconv.Atoi(kStr); err == nil && k > 0 {
			topK = k
		}
	}

	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "or"
	}
	if mode != "or" && mode != "and" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "mode must be 'or' or 'and'",
		})
		return
	}

	// Run the search and record in query log.
	searchStart := time.Now()
	response := idx.Search(q, topK, mode)
	latency := time.Since(searchStart)

	ql.Record(q, latency, len(response.Results))

	log.Printf("[SEARCH] %q → %d hits, top %d returned (%.2fms)",
		q, response.TotalHits, len(response.Results), response.TimeTakenMs)

	writeJSON(w, http.StatusOK, response)
}

// evalRequest is the JSON body for POST /eval.
//
// Example:
//
//	{
//	  "k": 10,
//	  "judgments": [
//	    {
//	      "query": "go programming",
//	      "relevant_urls": ["https://go.dev", "https://go.dev/doc"]
//	    }
//	  ]
//	}
type evalRequest struct {
	K          int                       `json:"k"`
	Judgments  []index.RelevanceJudgment  `json:"judgments"`
}

// handleEval handles POST /eval
//
// Runs evaluation against labeled relevance judgments and returns
// precision@K and recall for each query, plus mean aggregates.
func handleEval(w http.ResponseWriter, r *http.Request, idx *index.Index) {
	var req evalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid JSON: " + err.Error(),
		})
		return
	}

	if len(req.Judgments) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "judgments array is required and must not be empty",
		})
		return
	}

	if req.K <= 0 {
		req.K = 10
	}

	params := index.DefaultBM25()
	report := index.Evaluate(idx, req.Judgments, req.K, params)

	log.Printf("[EVAL] %d queries, mean precision@%d=%.3f, mean recall=%.3f",
		report.QueriesRun, req.K, report.MeanPrecision, report.MeanRecall)

	writeJSON(w, http.StatusOK, report)
}
