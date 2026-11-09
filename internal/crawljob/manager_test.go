package crawljob

import (
	"errors"
	"testing"
	"time"

	"github.com/Saurabh-1785/gocrawl/extractor"
)

func TestNormalize(t *testing.T) {
	r := Request{URL: "https://example.com"}
	if err := r.Normalize(); err != nil {
		t.Fatal(err)
	}
	if r.Depth != defaultDepth || r.MaxPages != defaultMaxPages || r.Workers != defaultWorkers {
		t.Errorf("defaults not applied: %+v", r)
	}

	for _, bad := range []Request{
		{Depth: maxDepth + 1},
		{Depth: -1},
		{MaxPages: maxMaxPages + 1},
		{Workers: maxWorkers + 1},
	} {
		if err := bad.Normalize(); err == nil {
			t.Errorf("%+v should be rejected", bad)
		}
	}
}

func TestPruneKeepsNewestFinished(t *testing.T) {
	jm := NewManager(1, 2)
	base := time.Unix(1000, 0)
	for i, id := range []string{"a", "b", "c", "d"} {
		done := base.Add(time.Duration(i) * time.Minute)
		jm.jobs[id] = &Job{ID: id, StartedAt: done, FinishedAt: &done}
	}
	jm.jobs["running"] = &Job{ID: "running", StartedAt: base}

	jm.pruneLocked()

	for _, id := range []string{"c", "d", "running"} {
		if jm.Get(id) == nil {
			t.Errorf("job %s should be kept", id)
		}
	}
	for _, id := range []string{"a", "b"} {
		if jm.Get(id) != nil {
			t.Errorf("job %s should be pruned", id)
		}
	}
}

func TestStartRejectsWhenFull(t *testing.T) {
	jm := NewManager(1, 10)
	jm.running = 1 // pretend one crawl is in progress
	_, err := jm.Start(Request{URL: "https://example.com"}, func(extractor.Document) {}, nil)
	if !errors.Is(err, ErrTooManyJobs) {
		t.Fatalf("expected ErrTooManyJobs, got %v", err)
	}
}
