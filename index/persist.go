package index

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// File names used for index persistence.
const (
	postingsFile   = "postings.bin"
	dictionaryFile = "dictionary.json"
	docsFile       = "docs.json"
	contentsFile   = "contents.bin"
)

// --- Dictionary JSON structures ---

// DictMeta holds metadata about the index, written into dictionary.json.
type DictMeta struct {
	DocCount  int `json:"doc_count"`
	TermCount int `json:"term_count"`
	Version   int `json:"version"`
}

// DictEntry stores the byte offset and length of a term's posting list
// in postings.bin, plus the document frequency.
type DictEntry struct {
	Offset int64  `json:"offset"` // byte offset into postings.bin
	Length int64  `json:"length"` // byte length of this term's postings block
	DF     uint32 `json:"df"`     // document frequency
}

// DictFile is the top-level structure of dictionary.json.
type DictFile struct {
	Meta  DictMeta             `json:"meta"`
	Terms map[string]DictEntry `json:"terms"`
}

// --- Save ---

// SaveIndex persists the index to disk as three files:
//   - postings.bin  — binary posting lists (8 bytes per posting, fixed-size)
//   - dictionary.json — term → {offset, length, df} + metadata
//   - docs.json — array of DocMeta for search result display
//
// The directory is created if it doesn't exist.
func SaveIndex(idx *Index, dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create index dir: %w", err)
	}

	// Hold write lock for the entire save — we mutate docTable (content offsets),
	// clear contentsBuf, and set contentDir.
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// --- 1. Write postings.bin ---
	postingsPath := filepath.Join(dir, postingsFile)
	pf, err := os.Create(postingsPath)
	if err != nil {
		return fmt.Errorf("create postings file: %w", err)
	}
	defer pf.Close()

	dict := idx.Dictionary()
	docTable := idx.DocTable()

	// Sort terms for deterministic output (easier to diff/debug).
	termNames := make([]string, 0, len(dict))
	for term := range dict {
		termNames = append(termNames, term)
	}
	sort.Strings(termNames)

	// Write each term's postings and record offset/length in dictionary.
	dictTerms := make(map[string]DictEntry, len(dict))
	var offset int64

	for _, term := range termNames {
		entry := dict[term]
		startOffset := offset

		for _, p := range entry.Postings {
			// Each posting: DocID (uint32, 4 bytes) + TF (uint32, 4 bytes) = 8 bytes
			if err := binary.Write(pf, binary.BigEndian, p.DocID); err != nil {
				return fmt.Errorf("write posting docID: %w", err)
			}
			if err := binary.Write(pf, binary.BigEndian, p.TF); err != nil {
				return fmt.Errorf("write posting TF: %w", err)
			}
			offset += 8
		}

		dictTerms[term] = DictEntry{
			Offset: startOffset,
			Length: offset - startOffset,
			DF:     entry.DF,
		}
	}

	// --- 2. Write dictionary.json ---
	dictFile := DictFile{
		Meta: DictMeta{
			DocCount:  len(docTable),
			TermCount: len(dict),
			Version:   1,
		},
		Terms: dictTerms,
	}

	dictPath := filepath.Join(dir, dictionaryFile)
	df, err := os.Create(dictPath)
	if err != nil {
		return fmt.Errorf("create dictionary file: %w", err)
	}
	defer df.Close()

	enc := json.NewEncoder(df)
	enc.SetIndent("", "  ")
	if err := enc.Encode(dictFile); err != nil {
		return fmt.Errorf("encode dictionary: %w", err)
	}

	// --- 3. Write contents.bin and populate DocMeta offsets ---
	contentsPath := filepath.Join(dir, contentsFile)
	cf, err := os.Create(contentsPath)
	if err != nil {
		return fmt.Errorf("create contents file: %w", err)
	}
	defer cf.Close()

	contentsBuf := idx.ContentsBuf()
	var contentOffset int64

	for i := range docTable {
		var contentBytes []byte
		if contentsBuf != nil && i < len(contentsBuf) {
			contentBytes = []byte(contentsBuf[i])
		}

		// Write content length prefix (uint32, 4 bytes)
		contentLen := uint32(len(contentBytes))
		if err := binary.Write(cf, binary.BigEndian, contentLen); err != nil {
			return fmt.Errorf("write content length: %w", err)
		}

		// Write raw content bytes
		if len(contentBytes) > 0 {
			if _, err := cf.Write(contentBytes); err != nil {
				return fmt.Errorf("write content: %w", err)
			}
		}

		// Record offset (pointing to start of length prefix) and content byte length
		docTable[i].ContentOffset = contentOffset + 4 // skip the 4-byte length prefix
		docTable[i].ContentLength = int64(contentLen)
		contentOffset += 4 + int64(contentLen)
	}

	// --- 4. Write docs.json (after content offsets are populated) ---
	docsPath := filepath.Join(dir, docsFile)
	docsF, err := os.Create(docsPath)
	if err != nil {
		return fmt.Errorf("create docs file: %w", err)
	}
	defer docsF.Close()

	docsEnc := json.NewEncoder(docsF)
	docsEnc.SetIndent("", "  ")
	if err := docsEnc.Encode(docTable); err != nil {
		return fmt.Errorf("encode docs: %w", err)
	}

	// Update internal state (safe — we hold the write lock).
	idx.docTable = docTable
	idx.contentsBuf = nil
	idx.contentDir = dir

	return nil
}

// --- Load ---

// LoadDictionary reads dictionary.json from disk into memory.
// This gives you the term→{offset, length, df} mapping needed to
// look up postings lazily from postings.bin.
func LoadDictionary(dir string) (*DictFile, error) {
	dictPath := filepath.Join(dir, dictionaryFile)
	f, err := os.Open(dictPath)
	if err != nil {
		return nil, fmt.Errorf("open dictionary: %w", err)
	}
	defer f.Close()

	var dictFile DictFile
	if err := json.NewDecoder(f).Decode(&dictFile); err != nil {
		return nil, fmt.Errorf("decode dictionary: %w", err)
	}

	return &dictFile, nil
}

// LoadDocTable reads docs.json from disk into memory.
// This gives you the DocID→metadata mapping needed to display search results.
func LoadDocTable(dir string) ([]DocMeta, error) {
	docsPath := filepath.Join(dir, docsFile)
	f, err := os.Open(docsPath)
	if err != nil {
		return nil, fmt.Errorf("open docs: %w", err)
	}
	defer f.Close()

	var docs []DocMeta
	if err := json.NewDecoder(f).Decode(&docs); err != nil {
		return nil, fmt.Errorf("decode docs: %w", err)
	}

	return docs, nil
}

// ReadPostings reads a specific term's posting list from postings.bin
// using the offset and length from the dictionary.
//
// This enables lazy loading — only read postings for terms that are
// actually queried, instead of loading the entire postings file.
func ReadPostings(dir string, offset, length int64) ([]Posting, error) {
	if length == 0 {
		return nil, nil
	}

	// Each posting is 8 bytes (4 DocID + 4 TF).
	if length%8 != 0 {
		return nil, fmt.Errorf("invalid postings block length %d (must be multiple of 8)", length)
	}

	postingsPath := filepath.Join(dir, postingsFile)
	f, err := os.Open(postingsPath)
	if err != nil {
		return nil, fmt.Errorf("open postings file: %w", err)
	}
	defer f.Close()

	// Seek to the term's block.
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek to offset %d: %w", offset, err)
	}

	numPostings := int(length / 8)
	postings := make([]Posting, numPostings)

	for i := 0; i < numPostings; i++ {
		if err := binary.Read(f, binary.BigEndian, &postings[i].DocID); err != nil {
			return nil, fmt.Errorf("read posting %d docID: %w", i, err)
		}
		if err := binary.Read(f, binary.BigEndian, &postings[i].TF); err != nil {
			return nil, fmt.Errorf("read posting %d TF: %w", i, err)
		}
	}

	return postings, nil
}

// ReadDocContent reads a single document's raw content from contents.bin.
// Uses the offset/length from DocMeta — O(1) seek, no full file scan.
// Only called for top-K results that need snippets.
func ReadDocContent(dir string, offset, length int64) (string, error) {
	if length == 0 {
		return "", nil
	}

	contentsPath := filepath.Join(dir, contentsFile)
	f, err := os.Open(contentsPath)
	if err != nil {
		return "", fmt.Errorf("open contents file: %w", err)
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", fmt.Errorf("seek to offset %d: %w", offset, err)
	}

	buf := make([]byte, length)
	if _, err := io.ReadFull(f, buf); err != nil {
		return "", fmt.Errorf("read content: %w", err)
	}

	return string(buf), nil
}
