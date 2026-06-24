package main

import (
	"log"
	"net/http"
	"strconv"

	"search-engine/index"
)

// registerSearchRoutes sets up the search API endpoints on the given mux.
func registerSearchRoutes(mux *http.ServeMux, idx *index.Index) {
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		handleSearch(w, r, idx)
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
//	      "score": 0.0847,
//	      "snippet": "...The <mark>Go</mark> <mark>Programming</mark> Language..."
//	    }
//	  ],
//	  "total_hits": 15,
//	  "time_taken_ms": 1.23
//	}
func handleSearch(w http.ResponseWriter, r *http.Request, idx *index.Index) {
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

	// Run the search.
	response := idx.Search(q, topK, mode)

	log.Printf("[SEARCH] %q → %d hits, top %d returned (%.2fms)",
		q, response.TotalHits, len(response.Results), response.TimeTakenMs)

	writeJSON(w, http.StatusOK, response)
}
