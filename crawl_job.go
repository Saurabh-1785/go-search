package main

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/Saurabh-1785/gocrawl/crawl"
	"github.com/Saurabh-1785/gocrawl/extractor"
)

// JobStatus represents the current state of a crawl job.
type JobStatus string

const (
	JobStatusPending   JobStatus = "pending"
	JobStatusRunning   JobStatus = "running"
	JobStatusCompleted JobStatus = "completed"
	JobStatusFailed    JobStatus = "failed"
)

// CrawlJob holds the state and result of a crawl job.
type CrawlJob struct {
	ID        string        `json:"job_id"`
	Status    JobStatus     `json:"status"`
	SeedURL   string        `json:"seed_url"`
	StartedAt time.Time     `json:"started_at"`
	Result    *CrawlJobResult `json:"result,omitempty"`
	Error     string        `json:"error,omitempty"`
}

// CrawlJobResult holds the final metrics from a completed crawl.
type CrawlJobResult struct {
	DocumentsWritten int    `json:"documents_written"`
	DocumentsSkipped int    `json:"documents_skipped"`
	PagesVisited     int    `json:"pages_visited"`
	PagesFailed      int    `json:"pages_failed"`
	Duration         string `json:"duration"`
	OutputPath       string `json:"output_path,omitempty"`
}

// CrawlRequest is the JSON body for POST /crawl.
type CrawlRequest struct {
	URL       string `json:"url"`
	Depth     int    `json:"depth"`
	MaxPages  int    `json:"max_pages"`
	Workers   int    `json:"workers"`
	OutputPath string `json:"output_path,omitempty"`
}

// JobManager manages crawl jobs in memory.
// Thread-safe: jobs can be started and queried concurrently.
type JobManager struct {
	mu   sync.RWMutex
	jobs map[string]*CrawlJob
}

// NewJobManager creates a new empty JobManager.
func NewJobManager() *JobManager {
	return &JobManager{
		jobs: make(map[string]*CrawlJob),
	}
}

// StartJob creates a new crawl job, spawns a goroutine to run it,
// and returns the job ID immediately.
//
// The onDocument callback is called for every extracted document —
// use this to pipe documents directly to your indexer.
//
// The onComplete callback is called after a successful crawl —
// use this for post-crawl actions like saving the index to disk.
func (jm *JobManager) StartJob(req CrawlRequest, onDocument func(doc extractor.Document), onComplete func()) string {
	jobID := generateJobID()

	job := &CrawlJob{
		ID:        jobID,
		Status:    JobStatusPending,
		SeedURL:   req.URL,
		StartedAt: time.Now(),
	}

	jm.mu.Lock()
	jm.jobs[jobID] = job
	jm.mu.Unlock()

	// Run the crawl in a background goroutine
	go func() {
		// Mark as running
		jm.mu.Lock()
		job.Status = JobStatusRunning
		jm.mu.Unlock()

		// Set up a buffered document channel so multiple crawler workers
		// can send documents concurrently without blocking each other.
		// A single consumer goroutine drains the channel and calls
		// onDocument serially — the indexer never needs to be thread-safe.
		docCh := make(chan extractor.Document, 64)

		var consumerWg sync.WaitGroup
		consumerWg.Add(1)
		go func() {
			defer consumerWg.Done()
			for doc := range docCh {
				if onDocument != nil {
					onDocument(doc)
				}
			}
		}()

		cfg := crawl.CrawlConfig{
			SeedURL:    req.URL,
			MaxDepth:   req.Depth,
			MaxPages:   req.MaxPages,
			Workers:    req.Workers,
			OutputPath: req.OutputPath,
			OnDocument: func(doc extractor.Document) {
				docCh <- doc
			},
		}

		result, err := crawl.Run(cfg)

		// All crawler workers are done — close the channel and wait
		// for the consumer to finish draining any remaining documents.
		close(docCh)
		consumerWg.Wait()

		jm.mu.Lock()
		defer jm.mu.Unlock()

		if err != nil {
			job.Status = JobStatusFailed
			job.Error = err.Error()
			return
		}

		job.Status = JobStatusCompleted
		job.Result = &CrawlJobResult{
			DocumentsWritten: result.DocumentsWritten,
			PagesVisited:     result.PagesVisited,
			PagesFailed:      result.PagesFailed,
			Duration:         result.Duration.Round(time.Millisecond).String(),
			OutputPath:       result.OutputPath,
		}

		// Run post-crawl actions (e.g., save index to disk)
		if onComplete != nil {
			onComplete()
		}
	}()

	return jobID
}

// GetJob returns a job by ID, or nil if not found.
func (jm *JobManager) GetJob(id string) *CrawlJob {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	job, ok := jm.jobs[id]
	if !ok {
		return nil
	}

	// Return a copy to avoid race conditions
	jobCopy := *job
	if job.Result != nil {
		resultCopy := *job.Result
		jobCopy.Result = &resultCopy
	}
	return &jobCopy
}

// ListJobs returns all jobs.
func (jm *JobManager) ListJobs() []CrawlJob {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	jobs := make([]CrawlJob, 0, len(jm.jobs))
	for _, job := range jm.jobs {
		jobCopy := *job
		if job.Result != nil {
			resultCopy := *job.Result
			jobCopy.Result = &resultCopy
		}
		jobs = append(jobs, jobCopy)
	}
	return jobs
}

// generateJobID creates a random 8-byte hex string (16 chars).
func generateJobID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
