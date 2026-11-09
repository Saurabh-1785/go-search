// Package crawljob runs crawls in the background and tracks their status.
package crawljob

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/Saurabh-1785/gocrawl/crawl"
	"github.com/Saurabh-1785/gocrawl/extractor"
)

// Status represents the current state of a crawl job.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
)

// Job holds the state and result of a crawl job.
type Job struct {
	ID         string     `json:"job_id"`
	Status     Status     `json:"status"`
	SeedURL    string     `json:"seed_url"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Result     *Result    `json:"result,omitempty"`
	Error      string     `json:"error,omitempty"`
}

// Result holds the final metrics from a completed crawl.
type Result struct {
	DocumentsIndexed int    `json:"documents_indexed"`
	PagesVisited     int    `json:"pages_visited"`
	PagesFailed      int    `json:"pages_failed"`
	Duration         string `json:"duration"`
}

// Request is the JSON body for POST /crawl.
//
// There is deliberately no output path: letting a caller choose a file the
// server writes to is an arbitrary file write. Documents go straight into the
// index, which is persisted under DATA_DIR.
type Request struct {
	URL      string `json:"url"`
	Depth    int    `json:"depth"`
	MaxPages int    `json:"max_pages"`
	Workers  int    `json:"workers"`
}

// Crawl request limits.
const (
	defaultDepth    = 2
	maxDepth        = 3
	defaultMaxPages = 50
	maxMaxPages     = 500
	defaultWorkers  = 3
	maxWorkers      = 8
)

// Normalize fills defaults and rejects out-of-range values.
func (r *Request) Normalize() error {
	if r.Depth == 0 {
		r.Depth = defaultDepth
	}
	if r.MaxPages == 0 {
		r.MaxPages = defaultMaxPages
	}
	if r.Workers == 0 {
		r.Workers = defaultWorkers
	}
	switch {
	case r.Depth < 1 || r.Depth > maxDepth:
		return fmt.Errorf("depth must be between 1 and %d", maxDepth)
	case r.MaxPages < 1 || r.MaxPages > maxMaxPages:
		return fmt.Errorf("max_pages must be between 1 and %d", maxMaxPages)
	case r.Workers < 1 || r.Workers > maxWorkers:
		return fmt.Errorf("workers must be between 1 and %d", maxWorkers)
	}
	return nil
}

// ErrTooManyJobs is returned by Start when the running-job cap is reached.
var ErrTooManyJobs = errors.New("too many crawl jobs running")

// Manager manages crawl jobs in memory.
// Thread-safe: jobs can be started and queried concurrently.
type Manager struct {
	mu         sync.RWMutex
	jobs       map[string]*Job
	running    int
	maxRunning int // concurrent crawls allowed
	maxHistory int // finished jobs kept for status queries
}

// NewManager creates a new empty Manager.
func NewManager(maxRunning, maxHistory int) *Manager {
	return &Manager{
		jobs:       make(map[string]*Job),
		maxRunning: maxRunning,
		maxHistory: maxHistory,
	}
}

// Start creates a new crawl job, spawns a goroutine to run it,
// and returns the job ID immediately.
//
// onDocument is called serially for every extracted document.
// onFinish is called once the crawl ends, whether it succeeded or not — any
// documents indexed before a failure are still worth saving.
func (jm *Manager) Start(req Request, onDocument func(doc extractor.Document), onFinish func()) (string, error) {
	jm.mu.Lock()
	if jm.running >= jm.maxRunning {
		jm.mu.Unlock()
		return "", ErrTooManyJobs
	}
	jobID := generateJobID()
	job := &Job{
		ID:        jobID,
		Status:    StatusRunning,
		SeedURL:   req.URL,
		StartedAt: time.Now(),
	}
	jm.jobs[jobID] = job
	jm.running++
	jm.pruneLocked()
	jm.mu.Unlock()

	go jm.run(job, req, onDocument, onFinish)
	return jobID, nil
}

func (jm *Manager) run(job *Job, req Request, onDocument func(doc extractor.Document), onFinish func()) {
	var (
		result  crawl.CrawlResult
		err     error
		indexed int
	)

	func() {
		// A panic in the crawler must not take down the whole server.
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("crawler panic: %v", p)
				log.Printf("[ERROR] job %s: %v", job.ID, err)
			}
		}()

		// Buffered channel so crawler workers don't block each other; a
		// single consumer calls onDocument serially.
		docCh := make(chan extractor.Document, 64)
		var consumerWg sync.WaitGroup
		consumerWg.Add(1)
		go func() {
			defer consumerWg.Done()
			for doc := range docCh {
				onDocument(doc)
				indexed++
			}
		}()
		defer func() {
			close(docCh)
			consumerWg.Wait()
		}()

		result, err = crawl.Run(crawl.CrawlConfig{
			SeedURL:    req.URL,
			MaxDepth:   req.Depth,
			MaxPages:   req.MaxPages,
			Workers:    req.Workers,
			OnDocument: func(doc extractor.Document) { docCh <- doc },
		})
	}()

	// Persist outside the job lock: saving can be slow and status queries
	// must not wait on it.
	if onFinish != nil {
		onFinish()
	}

	now := time.Now()
	jm.mu.Lock()
	defer jm.mu.Unlock()
	jm.running--
	job.FinishedAt = &now
	if err != nil {
		job.Status = StatusFailed
		job.Error = err.Error()
		return
	}
	job.Status = StatusCompleted
	job.Result = &Result{
		DocumentsIndexed: indexed,
		PagesVisited:     result.PagesVisited,
		PagesFailed:      result.PagesFailed,
		Duration:         result.Duration.Round(time.Millisecond).String(),
	}
}

// pruneLocked drops the oldest finished jobs beyond maxHistory so the job map
// cannot grow without bound. Caller holds jm.mu.
func (jm *Manager) pruneLocked() {
	finished := make([]*Job, 0, len(jm.jobs))
	for _, j := range jm.jobs {
		if j.FinishedAt != nil {
			finished = append(finished, j)
		}
	}
	if len(finished) <= jm.maxHistory {
		return
	}
	sort.Slice(finished, func(a, b int) bool { return finished[a].StartedAt.Before(finished[b].StartedAt) })
	for _, j := range finished[:len(finished)-jm.maxHistory] {
		delete(jm.jobs, j.ID)
	}
}

// Get returns a copy of a job by ID, or nil if not found.
func (jm *Manager) Get(id string) *Job {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	job, ok := jm.jobs[id]
	if !ok {
		return nil
	}
	c := copyJob(job)
	return &c
}

// List returns copies of all jobs, newest first.
func (jm *Manager) List() []Job {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	jobs := make([]Job, 0, len(jm.jobs))
	for _, job := range jm.jobs {
		jobs = append(jobs, copyJob(job))
	}
	sort.Slice(jobs, func(a, b int) bool { return jobs[a].StartedAt.After(jobs[b].StartedAt) })
	return jobs
}

func copyJob(job *Job) Job {
	c := *job
	if job.Result != nil {
		r := *job.Result
		c.Result = &r
	}
	if job.FinishedAt != nil {
		t := *job.FinishedAt
		c.FinishedAt = &t
	}
	return c
}

// generateJobID creates a random 8-byte hex string (16 chars).
func generateJobID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
