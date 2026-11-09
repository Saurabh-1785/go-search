package server

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"search-engine/internal/index"
	"search-engine/internal/tokenizer"
)

// Search request limits. Each query term fans out to its own scoring
// goroutine and k controls how many snippets are built (disk reads), so both
// are bounded.
const (
	maxQueryBytes    = 512
	maxQueryTerms    = 32
	defaultTopK      = 10
	maxTopK          = 100
	maxEvalJudgments = 100
)

// handleSearch handles GET /search
//
// Query parameters:
//
//	q     — search query (required, max 512 bytes / 32 terms)
//	k     — max results (default 10, max 100)
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
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if err := validateQuery(q); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	topK := defaultTopK
	if kStr := r.URL.Query().Get("k"); kStr != "" {
		k, err := strconv.Atoi(kStr)
		if err != nil || k < 1 || k > maxTopK {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("k must be an integer between 1 and %d", maxTopK))
			return
		}
		topK = k
	}

	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "or"
	}
	if mode != "or" && mode != "and" {
		writeError(w, http.StatusBadRequest, "mode must be 'or' or 'and'")
		return
	}

	searchStart := time.Now()
	response := s.idx.Search(q, topK, mode)
	latency := time.Since(searchStart)

	s.ql.Record(latency, len(response.Results))

	// The query text is not logged: it can contain personal data.
	log.Printf("[SEARCH] %d hits, top %d returned (%.2fms, cache_hit=%t)",
		response.TotalHits, len(response.Results), response.TimeTakenMs, response.CacheHit)

	writeJSON(w, http.StatusOK, response)
}

func validateQuery(q string) error {
	if q == "" {
		return fmt.Errorf("query parameter 'q' is required")
	}
	if len(q) > maxQueryBytes {
		return fmt.Errorf("query is too long (max %d bytes)", maxQueryBytes)
	}
	if n := len(tokenizer.Tokenize(q)); n > maxQueryTerms {
		return fmt.Errorf("query has too many terms (%d, max %d)", n, maxQueryTerms)
	}
	return nil
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
	K         int                       `json:"k"`
	Judgments []index.RelevanceJudgment `json:"judgments"`
}

// handleSearchStats handles GET /search/stats (admin only).
func (s *Server) handleSearchStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.ql.Stats())
}

// handleEval handles POST /eval (admin only).
//
// Runs evaluation against labeled relevance judgments and returns
// precision@K and recall for each query, plus mean aggregates.
func (s *Server) handleEval(w http.ResponseWriter, r *http.Request) {
	var req evalRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if len(req.Judgments) == 0 || len(req.Judgments) > maxEvalJudgments {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("judgments must contain between 1 and %d entries", maxEvalJudgments))
		return
	}
	for i, j := range req.Judgments {
		if err := validateQuery(j.Query); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("judgments[%d]: %v", i, err))
			return
		}
	}

	if req.K == 0 {
		req.K = defaultTopK
	}
	if req.K < 1 || req.K > maxTopK {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("k must be between 1 and %d", maxTopK))
		return
	}

	report := index.Evaluate(s.idx, req.Judgments, req.K)

	log.Printf("[EVAL] %d queries, mean precision@%d=%.3f, mean recall=%.3f",
		report.QueriesRun, req.K, report.MeanPrecision, report.MeanRecall)

	writeJSON(w, http.StatusOK, report)
}
