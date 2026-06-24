package index

import (
	"search-engine/tokenizer"
	"sync"
)

// Posting is a single entry in a posting list.
// 8 bytes total — compact enough to store millions in memory.
//
// DocID is an internal uint32 (not the SHA-256 hash from the crawler).
// The original hash lives in DocMeta, looked up via docTable[DocID].
type Posting struct {
	DocID uint32 // internal numeric document ID
	TF    uint32 // term frequency — how many times this term appears in this doc
}

// TermEntry holds the posting list and document frequency for a single term.
type TermEntry struct {
	DF       uint32    // document frequency — number of docs containing this term
	Postings []Posting // one entry per document that contains the term
}

// DocMeta stores metadata needed to display search results.
// The inverted index only stores uint32 DocIDs in postings —
// this table maps them back to human-readable info.
//
// ContentOffset and ContentLength point into contents.bin for raw text
// retrieval (used for snippet generation). Content is NOT stored in memory.
type DocMeta struct {
	DocID         uint32 `json:"doc_id"`
	ExternalID    string `json:"external_id"` // original SHA-256 hash from the crawler
	URL           string `json:"url"`
	Title         string `json:"title"`
	Length        int    `json:"length"`         // document content length in tokens
	ContentOffset int64  `json:"content_offset"` // byte offset into contents.bin
	ContentLength int64  `json:"content_length"` // byte length of raw content in contents.bin
}

// IndexStats holds summary statistics about the index.
type IndexStats struct {
	TermCount     int `json:"term_count"`
	DocCount      int `json:"doc_count"`
	TotalPostings int `json:"total_postings"`
}

// Index is the in-memory inverted index.
//
// Dictionary maps each stemmed token to its TermEntry (posting list + DF).
// DocTable maps uint32 DocIDs back to document metadata.
// ContentsBuf temporarily holds raw content during indexing — flushed to
// contents.bin on SaveIndex, then cleared to free memory.
//
// All methods are thread-safe — multiple goroutines can call AddDocument
// concurrently (e.g., from crawler workers via the document channel).
type Index struct {
	mu          sync.RWMutex
	dictionary  map[string]*TermEntry
	docTable    []DocMeta
	contentsBuf []string // temporary buffer, flushed to disk on SaveIndex
	nextDocID   uint32
	contentDir  string // directory where contents.bin lives (set after save/load)
}

// NewIndex creates an empty Index ready for document insertion.
func NewIndex() *Index {
	return &Index{
		dictionary:  make(map[string]*TermEntry),
		docTable:    make([]DocMeta, 0),
		contentsBuf: make([]string, 0),
	}
}

// AddDocument tokenizes the content, assigns an internal uint32 DocID,
// stores metadata in docTable, and appends postings for each unique term.
//
// The tokenization (CPU-heavy) runs outside the lock. The lock is held
// only for the fast map writes.
//
// Parameters:
//   - externalID: the SHA-256 document hash from the crawler
//   - url: the page URL (for search result display)
//   - title: the page title (for search result display)
//   - content: the raw text content to tokenize and index
func (idx *Index) AddDocument(externalID, url, title, content string) {
	// --- Phase 1: Tokenize outside the lock (CPU-heavy) ---
	tokens := tokenizer.Tokenize(content)

	// Count term frequencies in a local map.
	// This avoids taking the lock once per token.
	termFreqs := make(map[string]uint32)
	for _, tok := range tokens {
		termFreqs[tok]++
	}

	// --- Phase 2: Acquire lock, assign DocID, store metadata + postings ---
	idx.mu.Lock()
	defer idx.mu.Unlock()

	docID := idx.nextDocID
	idx.nextDocID++

	// Store document metadata.
	// ContentOffset/ContentLength are set to 0 here — they'll be
	// populated when SaveIndex writes contents.bin to disk.
	idx.docTable = append(idx.docTable, DocMeta{
		DocID:      docID,
		ExternalID: externalID,
		URL:        url,
		Title:      title,
		Length:     len(tokens),
	})

	// Buffer raw content for later flush to disk.
	idx.contentsBuf = append(idx.contentsBuf, content)

	// Append postings for each unique term
	for term, freq := range termFreqs {
		entry, exists := idx.dictionary[term]
		if !exists {
			entry = &TermEntry{
				Postings: make([]Posting, 0, 4),
			}
			idx.dictionary[term] = entry
		}
		entry.Postings = append(entry.Postings, Posting{
			DocID: docID,
			TF:    freq,
		})
		entry.DF++
	}
}

// GetPostings returns the TermEntry (posting list + DF) for a given token.
// The token is run through the same tokenizer pipeline (lowercase + stem)
// so that queries match indexed terms.
//
// Returns nil if the term is not in the index.
func (idx *Index) GetPostings(token string) *TermEntry {
	// Tokenize the query term through the same pipeline as indexed docs.
	// This handles lowercasing, stemming, and stopword filtering.
	terms := tokenizer.Tokenize(token)
	if len(terms) == 0 {
		return nil
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	entry, exists := idx.dictionary[terms[0]]
	if !exists {
		return nil
	}

	// Return a copy to avoid data races if the caller holds the
	// reference while another goroutine adds documents.
	copyEntry := &TermEntry{
		DF:       entry.DF,
		Postings: make([]Posting, len(entry.Postings)),
	}
	copy(copyEntry.Postings, entry.Postings)
	return copyEntry
}

// GetPostingsByTerm returns the TermEntry for an already-stemmed term.
// Use this when you've already tokenized the query yourself.
// Returns nil if the term is not in the index.
func (idx *Index) GetPostingsByTerm(term string) *TermEntry {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	entry, exists := idx.dictionary[term]
	if !exists {
		return nil
	}

	copyEntry := &TermEntry{
		DF:       entry.DF,
		Postings: make([]Posting, len(entry.Postings)),
	}
	copy(copyEntry.Postings, entry.Postings)
	return copyEntry
}

// GetDocMeta returns the metadata for a document by its internal uint32 ID.
// Returns nil if the ID is out of range.
func (idx *Index) GetDocMeta(docID uint32) *DocMeta {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if int(docID) >= len(idx.docTable) {
		return nil
	}

	// Return a copy
	meta := idx.docTable[docID]
	return &meta
}

// Terms returns all terms in the dictionary.
// Useful for debugging and iteration.
func (idx *Index) Terms() []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	terms := make([]string, 0, len(idx.dictionary))
	for term := range idx.dictionary {
		terms = append(terms, term)
	}
	return terms
}

// Stats returns summary statistics about the index.
func (idx *Index) Stats() IndexStats {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	totalPostings := 0
	for _, entry := range idx.dictionary {
		totalPostings += len(entry.Postings)
	}

	return IndexStats{
		TermCount:     len(idx.dictionary),
		DocCount:      len(idx.docTable),
		TotalPostings: totalPostings,
	}
}

// Dictionary returns the raw dictionary map. Used by SaveIndex for persistence.
// Caller must hold at least an RLock (or call under Stats/Save which do).
func (idx *Index) Dictionary() map[string]*TermEntry {
	return idx.dictionary
}

// DocTable returns the raw doc table slice. Used by SaveIndex for persistence.
func (idx *Index) DocTable() []DocMeta {
	return idx.docTable
}

// SetDocTable replaces the doc table. Used after loading from disk
// when content offsets need to be applied.
func (idx *Index) SetDocTable(table []DocMeta) {
	idx.docTable = table
}

// ContentsBuf returns the raw content buffer. Used by SaveIndex.
func (idx *Index) ContentsBuf() []string {
	return idx.contentsBuf
}

// ClearContentsBuf frees the content buffer after it's been flushed to disk.
func (idx *Index) ClearContentsBuf() {
	idx.contentsBuf = nil
}

// ContentDir returns the directory where contents.bin is stored.
func (idx *Index) ContentDir() string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.contentDir
}

// SetContentDir sets the directory where contents.bin is stored.
func (idx *Index) SetContentDir(dir string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.contentDir = dir
}

// GetDocContent reads a document's raw content from the content buffer
// (during build) or from contents.bin on disk (after save).
// Returns empty string if content is not available.
func (idx *Index) GetDocContent(docID uint32) string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if int(docID) >= len(idx.docTable) {
		return ""
	}

	// If content buffer still has data (pre-save), use it.
	if idx.contentsBuf != nil && int(docID) < len(idx.contentsBuf) {
		return idx.contentsBuf[docID]
	}

	// Otherwise, read from disk.
	meta := idx.docTable[docID]
	if idx.contentDir == "" || meta.ContentLength == 0 {
		return ""
	}

	content, err := ReadDocContent(idx.contentDir, meta.ContentOffset, meta.ContentLength)
	if err != nil {
		return ""
	}
	return content
}

// RLock acquires a read lock on the index. Used by persistence layer.
func (idx *Index) RLock() {
	idx.mu.RLock()
}

// RUnlock releases the read lock. Used by persistence layer.
func (idx *Index) RUnlock() {
	idx.mu.RUnlock()
}
