package index

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

// --- Core Index Tests ---

func TestAddAndGetPostings(t *testing.T) {
	idx := NewIndex()

	idx.AddDocument("hash1", "https://example.com/1", "Page One", "Go is a programming language")
	idx.AddDocument("hash2", "https://example.com/2", "Page Two", "Go is fast and concurrent")
	idx.AddDocument("hash3", "https://example.com/3", "Page Three", "Python is a programming language too")

	// "go" should appear in doc 0 and doc 1
	entry := idx.GetPostings("Go")
	if entry == nil {
		t.Fatal("expected postings for 'Go', got nil")
	}
	if len(entry.Postings) != 2 {
		t.Fatalf("expected 2 postings for 'Go', got %d", len(entry.Postings))
	}
	if entry.Postings[0].DocID != 0 || entry.Postings[1].DocID != 1 {
		t.Errorf("expected DocIDs [0, 1], got [%d, %d]",
			entry.Postings[0].DocID, entry.Postings[1].DocID)
	}

	// Each doc has "go" exactly once → TF = 1
	for _, p := range entry.Postings {
		if p.TF != 1 {
			t.Errorf("expected TF=1 for DocID %d, got %d", p.DocID, p.TF)
		}
	}

	// "program" (stemmed from "programming") should appear in doc 0 and doc 2
	entry = idx.GetPostings("programming")
	if entry == nil {
		t.Fatal("expected postings for 'programming', got nil")
	}
	if len(entry.Postings) != 2 {
		t.Fatalf("expected 2 postings for 'programming', got %d", len(entry.Postings))
	}
	if entry.Postings[0].DocID != 0 || entry.Postings[1].DocID != 2 {
		t.Errorf("expected DocIDs [0, 2], got [%d, %d]",
			entry.Postings[0].DocID, entry.Postings[1].DocID)
	}
}

func TestDocumentFrequency(t *testing.T) {
	idx := NewIndex()

	idx.AddDocument("h1", "u1", "t1", "go go go")          // "go" appears 3 times
	idx.AddDocument("h2", "u2", "t2", "go is great")        // "go" appears 1 time
	idx.AddDocument("h3", "u3", "t3", "python is great")    // "go" doesn't appear

	entry := idx.GetPostings("go")
	if entry == nil {
		t.Fatal("expected postings for 'go', got nil")
	}

	// DF should be 2 (two docs contain "go")
	if entry.DF != 2 {
		t.Errorf("expected DF=2 for 'go', got %d", entry.DF)
	}

	// Verify TF values
	if entry.Postings[0].TF != 3 {
		t.Errorf("expected TF=3 for doc 0, got %d", entry.Postings[0].TF)
	}
	if entry.Postings[1].TF != 1 {
		t.Errorf("expected TF=1 for doc 1, got %d", entry.Postings[1].TF)
	}

	// "great" stemmed should appear in doc 1 and doc 2
	entry = idx.GetPostings("great")
	if entry == nil {
		t.Fatal("expected postings for 'great', got nil")
	}
	if entry.DF != 2 {
		t.Errorf("expected DF=2 for 'great', got %d", entry.DF)
	}
}

func TestDocTable(t *testing.T) {
	idx := NewIndex()

	idx.AddDocument("sha256hash1", "https://go.dev", "The Go Programming Language", "Go is awesome")
	idx.AddDocument("sha256hash2", "https://python.org", "Python", "Python is great too")

	// Doc 0
	meta := idx.GetDocMeta(0)
	if meta == nil {
		t.Fatal("expected DocMeta for ID 0, got nil")
	}
	if meta.ExternalID != "sha256hash1" {
		t.Errorf("expected ExternalID 'sha256hash1', got %q", meta.ExternalID)
	}
	if meta.URL != "https://go.dev" {
		t.Errorf("expected URL 'https://go.dev', got %q", meta.URL)
	}
	if meta.Title != "The Go Programming Language" {
		t.Errorf("expected Title 'The Go Programming Language', got %q", meta.Title)
	}
	if meta.Length <= 0 {
		t.Errorf("expected positive Length, got %d", meta.Length)
	}

	// Doc 1
	meta = idx.GetDocMeta(1)
	if meta == nil {
		t.Fatal("expected DocMeta for ID 1, got nil")
	}
	if meta.URL != "https://python.org" {
		t.Errorf("expected URL 'https://python.org', got %q", meta.URL)
	}

	// Out of range
	meta = idx.GetDocMeta(999)
	if meta != nil {
		t.Errorf("expected nil for out-of-range DocID 999, got %+v", meta)
	}
}

func TestUint32DocIDs(t *testing.T) {
	idx := NewIndex()

	// Add 5 documents, verify DocIDs are sequential 0..4
	for i := 0; i < 5; i++ {
		idx.AddDocument("hash", "url", "title", "some content here")
	}

	for i := uint32(0); i < 5; i++ {
		meta := idx.GetDocMeta(i)
		if meta == nil {
			t.Fatalf("expected DocMeta for ID %d, got nil", i)
		}
		if meta.DocID != i {
			t.Errorf("expected DocID %d, got %d", i, meta.DocID)
		}
	}
}

func TestTokenizerIntegration(t *testing.T) {
	idx := NewIndex()

	// "running" in the document should match a query for "runs" (both stem to "run")
	idx.AddDocument("h1", "u1", "t1", "The runner is running fast")
	idx.AddDocument("h2", "u2", "t2", "She runs every morning")

	// Query "running" → stemmed to "run"
	entry := idx.GetPostings("running")
	if entry == nil {
		t.Fatal("expected postings for 'running' (stemmed to 'run'), got nil")
	}
	if len(entry.Postings) != 2 {
		t.Fatalf("expected 2 postings (both docs contain forms of 'run'), got %d", len(entry.Postings))
	}

	// Query "runs" → also stemmed to "run"
	entry2 := idx.GetPostings("runs")
	if entry2 == nil {
		t.Fatal("expected postings for 'runs', got nil")
	}
	if len(entry2.Postings) != 2 {
		t.Fatalf("expected 2 postings for 'runs', got %d", len(entry2.Postings))
	}
}

func TestEmptyIndex(t *testing.T) {
	idx := NewIndex()

	entry := idx.GetPostings("anything")
	if entry != nil {
		t.Errorf("expected nil for empty index, got %+v", entry)
	}

	stats := idx.Stats()
	if stats.TermCount != 0 || stats.DocCount != 0 || stats.TotalPostings != 0 {
		t.Errorf("expected all-zero stats, got %+v", stats)
	}
}

func TestStopwordsFiltered(t *testing.T) {
	idx := NewIndex()

	// "the", "is", "a" are stopwords — they should not appear in the index
	idx.AddDocument("h1", "u1", "t1", "the cat is a cat")

	entry := idx.GetPostings("the")
	if entry != nil {
		t.Errorf("expected nil for stopword 'the', got %+v", entry)
	}

	entry = idx.GetPostings("is")
	if entry != nil {
		t.Errorf("expected nil for stopword 'is', got %+v", entry)
	}

	// "cat" should be indexed with TF=2
	entry = idx.GetPostings("cat")
	if entry == nil {
		t.Fatal("expected postings for 'cat', got nil")
	}
	if entry.Postings[0].TF != 2 {
		t.Errorf("expected TF=2 for 'cat', got %d", entry.Postings[0].TF)
	}
}

func TestStats(t *testing.T) {
	idx := NewIndex()

	idx.AddDocument("h1", "u1", "t1", "go programming")
	idx.AddDocument("h2", "u2", "t2", "go concurrency")

	stats := idx.Stats()
	if stats.DocCount != 2 {
		t.Errorf("expected DocCount=2, got %d", stats.DocCount)
	}
	if stats.TermCount <= 0 {
		t.Errorf("expected positive TermCount, got %d", stats.TermCount)
	}
	if stats.TotalPostings <= 0 {
		t.Errorf("expected positive TotalPostings, got %d", stats.TotalPostings)
	}
}

func TestTerms(t *testing.T) {
	idx := NewIndex()

	idx.AddDocument("h1", "u1", "t1", "go programming language")

	terms := idx.Terms()
	if len(terms) == 0 {
		t.Fatal("expected non-empty terms list")
	}

	// Sort for deterministic comparison
	sort.Strings(terms)

	// "go", "program" (stemmed), "languag" (stemmed) — after stopword removal
	// The exact stems depend on the Porter stemmer
	found := make(map[string]bool)
	for _, term := range terms {
		found[term] = true
	}

	if !found["go"] {
		t.Error("expected 'go' in terms")
	}
}

func TestGetPostingsByTerm(t *testing.T) {
	idx := NewIndex()

	idx.AddDocument("h1", "u1", "t1", "go programming")

	// GetPostingsByTerm uses already-stemmed terms
	entry := idx.GetPostingsByTerm("go")
	if entry == nil {
		t.Fatal("expected postings for stemmed term 'go', got nil")
	}

	// Non-existent term
	entry = idx.GetPostingsByTerm("nonexistent")
	if entry != nil {
		t.Errorf("expected nil for nonexistent term, got %+v", entry)
	}
}

// --- Concurrency Test ---

func TestConcurrentAdds(t *testing.T) {
	idx := NewIndex()

	var wg sync.WaitGroup
	numDocs := 100

	for i := 0; i < numDocs; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			idx.AddDocument("hash", "url", "title",
				"go programming language concurrency goroutines channels")
		}(i)
	}

	wg.Wait()

	stats := idx.Stats()
	if stats.DocCount != numDocs {
		t.Errorf("expected DocCount=%d, got %d", numDocs, stats.DocCount)
	}

	// "go" should appear in all documents
	entry := idx.GetPostings("go")
	if entry == nil {
		t.Fatal("expected postings for 'go', got nil")
	}
	if len(entry.Postings) != numDocs {
		t.Errorf("expected %d postings for 'go', got %d", numDocs, len(entry.Postings))
	}
	if entry.DF != uint32(numDocs) {
		t.Errorf("expected DF=%d, got %d", numDocs, entry.DF)
	}
}

// --- Persistence Tests ---

func TestSaveAndLoad(t *testing.T) {
	idx := NewIndex()

	idx.AddDocument("sha256_aaa", "https://go.dev", "Go Lang", "Go is a fast programming language")
	idx.AddDocument("sha256_bbb", "https://rust-lang.org", "Rust", "Rust is a systems programming language")
	idx.AddDocument("sha256_ccc", "https://python.org", "Python", "Python is great for scripting")

	// Save to temp directory
	dir := t.TempDir()
	if err := SaveIndex(idx, dir); err != nil {
		t.Fatalf("SaveIndex failed: %v", err)
	}

	// Verify all 3 files exist
	for _, name := range []string{postingsFile, dictionaryFile, docsFile} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Fatalf("expected file %s to exist", name)
		}
	}

	// Load dictionary
	dictFile, err := LoadDictionary(dir)
	if err != nil {
		t.Fatalf("LoadDictionary failed: %v", err)
	}
	if dictFile.Meta.DocCount != 3 {
		t.Errorf("expected DocCount=3, got %d", dictFile.Meta.DocCount)
	}
	if dictFile.Meta.Version != 1 {
		t.Errorf("expected Version=1, got %d", dictFile.Meta.Version)
	}

	// Load doc table
	docs, err := LoadDocTable(dir)
	if err != nil {
		t.Fatalf("LoadDocTable failed: %v", err)
	}
	if len(docs) != 3 {
		t.Fatalf("expected 3 docs, got %d", len(docs))
	}
	if docs[0].URL != "https://go.dev" {
		t.Errorf("expected first doc URL 'https://go.dev', got %q", docs[0].URL)
	}
	if docs[0].ExternalID != "sha256_aaa" {
		t.Errorf("expected first doc ExternalID 'sha256_aaa', got %q", docs[0].ExternalID)
	}
	if docs[1].Title != "Rust" {
		t.Errorf("expected second doc Title 'Rust', got %q", docs[1].Title)
	}

	// Read postings for a term that should exist
	// "program" (stemmed from "programming") should be in doc 0 and doc 1
	programEntry, ok := dictFile.Terms["program"]
	if !ok {
		// Try the actual stemmed form
		for term, entry := range dictFile.Terms {
			t.Logf("  dictionary term: %q → offset=%d length=%d df=%d", term, entry.Offset, entry.Length, entry.DF)
		}
		t.Fatal("expected 'program' in dictionary")
	}

	postings, err := ReadPostings(dir, programEntry.Offset, programEntry.Length)
	if err != nil {
		t.Fatalf("ReadPostings failed: %v", err)
	}
	if len(postings) != 2 {
		t.Fatalf("expected 2 postings for 'program', got %d", len(postings))
	}

	// Verify the round-trip: DocIDs should be 0 and 1
	docIDs := []uint32{postings[0].DocID, postings[1].DocID}
	sort.Slice(docIDs, func(i, j int) bool { return docIDs[i] < docIDs[j] })
	if docIDs[0] != 0 || docIDs[1] != 1 {
		t.Errorf("expected DocIDs [0, 1], got %v", docIDs)
	}

	// Verify DF was persisted
	if programEntry.DF != 2 {
		t.Errorf("expected DF=2 for 'program', got %d", programEntry.DF)
	}
}

func TestBinaryPostingSize(t *testing.T) {
	idx := NewIndex()

	// Add 3 docs, each containing "go" → 3 postings for "go"
	idx.AddDocument("h1", "u1", "t1", "go")
	idx.AddDocument("h2", "u2", "t2", "go")
	idx.AddDocument("h3", "u3", "t3", "go")

	dir := t.TempDir()
	if err := SaveIndex(idx, dir); err != nil {
		t.Fatalf("SaveIndex failed: %v", err)
	}

	// Read the raw binary file
	postingsPath := filepath.Join(dir, postingsFile)
	data, err := os.ReadFile(postingsPath)
	if err != nil {
		t.Fatalf("read postings.bin: %v", err)
	}

	// Total postings across all terms = sum of all posting list lengths.
	// "go" has 3 postings. There might be other terms too depending on tokenizer.
	// But each posting must be exactly 8 bytes.
	if len(data)%8 != 0 {
		t.Errorf("postings.bin size %d is not a multiple of 8", len(data))
	}

	// Verify we can read back valid data
	numPostings := len(data) / 8
	if numPostings < 3 {
		t.Errorf("expected at least 3 postings in binary file, got %d", numPostings)
	}

	// Read the first posting manually and verify it's a valid uint32 pair
	docID := binary.BigEndian.Uint32(data[0:4])
	tf := binary.BigEndian.Uint32(data[4:8])
	if tf == 0 {
		t.Error("expected non-zero TF in first posting")
	}
	t.Logf("first posting: DocID=%d TF=%d", docID, tf)
}

func TestSaveEmptyIndex(t *testing.T) {
	idx := NewIndex()

	dir := t.TempDir()
	if err := SaveIndex(idx, dir); err != nil {
		t.Fatalf("SaveIndex failed on empty index: %v", err)
	}

	// Verify files exist even for empty index
	dictFile, err := LoadDictionary(dir)
	if err != nil {
		t.Fatalf("LoadDictionary failed: %v", err)
	}
	if dictFile.Meta.DocCount != 0 {
		t.Errorf("expected DocCount=0, got %d", dictFile.Meta.DocCount)
	}
	if len(dictFile.Terms) != 0 {
		t.Errorf("expected 0 terms, got %d", len(dictFile.Terms))
	}

	docs, err := LoadDocTable(dir)
	if err != nil {
		t.Fatalf("LoadDocTable failed: %v", err)
	}
	if len(docs) != 0 {
		t.Errorf("expected 0 docs, got %d", len(docs))
	}
}

func TestReadPostingsInvalidLength(t *testing.T) {
	// Length not a multiple of 8 should error
	dir := t.TempDir()

	// Create a dummy postings file
	f, _ := os.Create(filepath.Join(dir, postingsFile))
	f.Write([]byte{0, 0, 0, 1, 0, 0, 0, 2, 0}) // 9 bytes — invalid
	f.Close()

	_, err := ReadPostings(dir, 0, 9)
	if err == nil {
		t.Error("expected error for length=9 (not multiple of 8), got nil")
	}
}
