package index

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// File names used for index persistence.
const (
	postingsFile   = "postings.bin"
	dictionaryFile = "dictionary.json"
	docsFile       = "docs.json"
	contentsFile   = "contents.bin"

	// currentFile names the active generation directory. It is replaced
	// atomically (write temp + rename), so readers always see either the old
	// or the new generation, never a half-written one.
	currentFile = "CURRENT"
	genPrefix   = "gen-"
)

// ErrNoIndex is returned by LoadIndex when the directory holds no saved index.
var ErrNoIndex = errors.New("no saved index")

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

// SaveIndex persists the index to disk.
//
// Layout:
//
//	dir/CURRENT          — name of the active generation, e.g. "gen-000004"
//	dir/gen-000004/      — postings.bin, dictionary.json, docs.json, contents.bin
//
// Each save writes a complete new generation directory, fsyncs it, then
// atomically swaps CURRENT. A crash at any point leaves the previous
// generation intact. The previous generation is kept as a fallback; older
// ones are deleted.
//
// The index is snapshotted under a read lock, so searches keep running while
// files are written. Documents added during the save are kept in memory and
// picked up by the next save.
func SaveIndex(idx *Index, dir string) error {
	idx.saveMu.Lock()
	defer idx.saveMu.Unlock()

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create index dir: %w", err)
	}

	// --- 1. Snapshot under the read lock (CPU only, no disk I/O) ---
	idx.mu.RLock()
	n := len(idx.docTable)
	docs := make([]DocMeta, n)
	copy(docs, idx.docTable)
	oldDir := idx.contentDir

	pending := make(map[uint32]string, len(idx.pending))
	for id, c := range idx.pending {
		if int(id) < n {
			pending[id] = c
		}
	}

	termNames := make([]string, 0, len(idx.dictionary))
	for term := range idx.dictionary {
		termNames = append(termNames, term)
	}
	sort.Strings(termNames)

	type encodedTerm struct {
		data []byte
		df   uint32
	}
	encoded := make(map[string]encodedTerm, len(termNames))
	for _, term := range termNames {
		// Postings are appended in DocID order, so docs added after the
		// snapshot sit at the tail of the list. Keep only the prefix < n.
		postings := idx.dictionary[term].Postings
		cut := sort.Search(len(postings), func(i int) bool { return int(postings[i].DocID) >= n })
		if cut == 0 {
			continue
		}
		encoded[term] = encodedTerm{data: EncodePostings(postings[:cut]), df: uint32(cut)}
	}
	idx.mu.RUnlock()

	// --- 2. Write a new generation directory ---
	gen, err := nextGeneration(dir)
	if err != nil {
		return err
	}
	genName := fmt.Sprintf("%s%06d", genPrefix, gen)
	genDir := filepath.Join(dir, genName)
	if err := os.RemoveAll(genDir); err != nil { // leftover from a crashed save
		return fmt.Errorf("clear generation dir: %w", err)
	}
	if err := os.MkdirAll(genDir, 0755); err != nil {
		return fmt.Errorf("create generation dir: %w", err)
	}
	fail := func(err error) error {
		os.RemoveAll(genDir)
		return err
	}

	// postings.bin + dictionary.json
	dictTerms := make(map[string]DictEntry, len(encoded))
	err = writeFileSync(filepath.Join(genDir, postingsFile), func(w io.Writer) error {
		var offset int64
		for _, term := range termNames {
			et, ok := encoded[term]
			if !ok {
				continue
			}
			if _, err := w.Write(et.data); err != nil {
				return fmt.Errorf("write postings for %q: %w", term, err)
			}
			dictTerms[term] = DictEntry{Offset: offset, Length: int64(len(et.data)), DF: et.df}
			offset += int64(len(et.data))
		}
		return nil
	})
	if err != nil {
		return fail(err)
	}

	dictFile := DictFile{
		Meta:  DictMeta{DocCount: n, TermCount: len(dictTerms), Version: 2},
		Terms: dictTerms,
	}
	if err := writeJSONSync(filepath.Join(genDir, dictionaryFile), dictFile); err != nil {
		return fail(err)
	}

	// contents.bin — content comes from the pending buffer, or is copied from
	// the previous generation's contents.bin for docs already on disk.
	var oldContents *os.File
	if oldDir != "" {
		if f, err := os.Open(filepath.Join(oldDir, contentsFile)); err == nil {
			oldContents = f
			defer oldContents.Close()
		}
	}
	err = writeFileSync(filepath.Join(genDir, contentsFile), func(w io.Writer) error {
		var offset int64
		for i := range docs {
			var content []byte
			if c, ok := pending[uint32(i)]; ok {
				content = []byte(c)
			} else if docs[i].ContentLength > 0 && oldContents != nil {
				content = make([]byte, docs[i].ContentLength)
				if _, err := oldContents.ReadAt(content, docs[i].ContentOffset); err != nil {
					return fmt.Errorf("copy content of doc %d: %w", i, err)
				}
			}

			// 4-byte big-endian length prefix, then raw bytes.
			if err := binary.Write(w, binary.BigEndian, uint32(len(content))); err != nil {
				return fmt.Errorf("write content length: %w", err)
			}
			if _, err := w.Write(content); err != nil {
				return fmt.Errorf("write content: %w", err)
			}
			docs[i].ContentOffset = offset + 4 // skip the length prefix
			docs[i].ContentLength = int64(len(content))
			offset += 4 + int64(len(content))
		}
		return nil
	})
	if err != nil {
		return fail(err)
	}

	// docs.json (after content offsets are populated)
	if err := writeJSONSync(filepath.Join(genDir, docsFile), docs); err != nil {
		return fail(err)
	}
	syncDir(genDir)

	// --- 3. Atomically publish the new generation ---
	if err := writeCurrent(dir, genName); err != nil {
		return fail(err)
	}

	// --- 4. Point the in-memory index at the new generation ---
	idx.mu.Lock()
	for i := 0; i < n; i++ {
		idx.docTable[i].ContentOffset = docs[i].ContentOffset
		idx.docTable[i].ContentLength = docs[i].ContentLength
		delete(idx.pending, uint32(i))
	}
	idx.contentDir = genDir
	if n > idx.savedDocs {
		idx.savedDocs = n
	}
	idx.mu.Unlock()

	// --- 5. Clean up generations older than the previous one ---
	cleanupGenerations(dir, genDir, oldDir)
	return nil
}

// LoadIndex rebuilds an in-memory Index from the generation named by
// dir/CURRENT. It also reads the older flat layout (files directly in dir).
// Returns an error wrapping ErrNoIndex if nothing has been saved yet.
func LoadIndex(dir string) (*Index, error) {
	genDir, err := resolveIndexDir(dir)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(genDir, dictionaryFile)); errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w in %s", ErrNoIndex, dir)
	}

	dict, err := loadDictionary(genDir)
	if err != nil {
		return nil, err
	}
	docs, err := loadDocTable(genDir)
	if err != nil {
		return nil, err
	}
	if len(docs) != dict.Meta.DocCount {
		return nil, fmt.Errorf("index corrupt: docs.json has %d docs, dictionary says %d", len(docs), dict.Meta.DocCount)
	}
	for i, d := range docs {
		if int(d.DocID) != i {
			return nil, fmt.Errorf("index corrupt: doc at position %d has id %d", i, d.DocID)
		}
	}

	postingsData, err := os.ReadFile(filepath.Join(genDir, postingsFile))
	if err != nil {
		return nil, fmt.Errorf("read postings: %w", err)
	}

	idx := NewIndex()
	for term, e := range dict.Terms {
		if e.Offset < 0 || e.Length < 0 || e.Offset+e.Length > int64(len(postingsData)) {
			return nil, fmt.Errorf("index corrupt: postings for %q out of range", term)
		}
		postings, err := DecodePostings(postingsData[e.Offset:e.Offset+e.Length], int(e.DF))
		if err != nil {
			return nil, fmt.Errorf("decode postings for %q: %w", term, err)
		}
		for _, p := range postings {
			if int(p.DocID) >= len(docs) {
				return nil, fmt.Errorf("index corrupt: term %q references doc %d", term, p.DocID)
			}
		}
		idx.dictionary[term] = &TermEntry{DF: e.DF, Postings: postings}
	}

	idx.docTable = docs
	idx.nextDocID = uint32(len(docs))
	idx.savedDocs = len(docs)
	idx.contentDir = genDir
	return idx, nil
}

// --- Read helpers ---
// The exported helpers accept the index root directory (the one passed to
// SaveIndex) and resolve the active generation.

// LoadDictionary reads dictionary.json of the active generation.
func LoadDictionary(dir string) (*DictFile, error) {
	genDir, err := resolveIndexDir(dir)
	if err != nil {
		return nil, err
	}
	return loadDictionary(genDir)
}

// LoadDocTable reads docs.json of the active generation.
func LoadDocTable(dir string) ([]DocMeta, error) {
	genDir, err := resolveIndexDir(dir)
	if err != nil {
		return nil, err
	}
	return loadDocTable(genDir)
}

// ReadPostings reads one term's compressed posting list from postings.bin
// using the offset and length from the dictionary. df is the number of
// postings to decode.
func ReadPostings(dir string, offset, length int64, df uint32) ([]Posting, error) {
	if length == 0 {
		return nil, nil
	}
	genDir, err := resolveIndexDir(dir)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(filepath.Join(genDir, postingsFile))
	if err != nil {
		return nil, fmt.Errorf("open postings file: %w", err)
	}
	defer f.Close()

	data := make([]byte, length)
	if _, err := f.ReadAt(data, offset); err != nil {
		return nil, fmt.Errorf("read compressed postings at %d: %w", offset, err)
	}
	return DecodePostings(data, int(df))
}

// ReadDocContent reads one document's raw content from contents.bin of the
// active generation, using the offset/length from DocMeta.
func ReadDocContent(dir string, offset, length int64) (string, error) {
	if length == 0 {
		return "", nil
	}
	genDir, err := resolveIndexDir(dir)
	if err != nil {
		return "", err
	}
	return readDocContent(genDir, offset, length)
}

// readDocContent reads content from contents.bin inside genDir (no resolve).
func readDocContent(genDir string, offset, length int64) (string, error) {
	if length == 0 {
		return "", nil
	}
	f, err := os.Open(filepath.Join(genDir, contentsFile))
	if err != nil {
		return "", fmt.Errorf("open contents file: %w", err)
	}
	defer f.Close()

	buf := make([]byte, length)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return "", fmt.Errorf("read content at %d: %w", offset, err)
	}
	return string(buf), nil
}

func loadDictionary(genDir string) (*DictFile, error) {
	f, err := os.Open(filepath.Join(genDir, dictionaryFile))
	if err != nil {
		return nil, fmt.Errorf("open dictionary: %w", err)
	}
	defer f.Close()

	var dictFile DictFile
	if err := json.NewDecoder(bufio.NewReader(f)).Decode(&dictFile); err != nil {
		return nil, fmt.Errorf("decode dictionary: %w", err)
	}
	return &dictFile, nil
}

func loadDocTable(genDir string) ([]DocMeta, error) {
	f, err := os.Open(filepath.Join(genDir, docsFile))
	if err != nil {
		return nil, fmt.Errorf("open docs: %w", err)
	}
	defer f.Close()

	var docs []DocMeta
	if err := json.NewDecoder(bufio.NewReader(f)).Decode(&docs); err != nil {
		return nil, fmt.Errorf("decode docs: %w", err)
	}
	return docs, nil
}

// --- Generation management ---

// resolveIndexDir returns the directory holding the active index files:
// dir/<CURRENT> if CURRENT exists, otherwise dir itself (older flat layout).
func resolveIndexDir(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, currentFile))
	if errors.Is(err, fs.ErrNotExist) {
		return dir, nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", currentFile, err)
	}
	name := strings.TrimSpace(string(data))
	if !strings.HasPrefix(name, genPrefix) || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("index corrupt: invalid %s contents %q", currentFile, name)
	}
	return filepath.Join(dir, name), nil
}

// nextGeneration returns one more than the highest generation number in dir.
func nextGeneration(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("list index dir: %w", err)
	}
	maxGen := 0
	for _, e := range entries {
		if g, ok := parseGeneration(e.Name()); ok && g > maxGen {
			maxGen = g
		}
	}
	return maxGen + 1, nil
}

func parseGeneration(name string) (int, bool) {
	if !strings.HasPrefix(name, genPrefix) {
		return 0, false
	}
	g, err := strconv.Atoi(strings.TrimPrefix(name, genPrefix))
	return g, err == nil
}

// writeCurrent atomically replaces dir/CURRENT with genName.
func writeCurrent(dir, genName string) error {
	tmp := filepath.Join(dir, currentFile+".tmp")
	err := writeFileSync(tmp, func(w io.Writer) error {
		_, err := io.WriteString(w, genName+"\n")
		return err
	})
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, currentFile)); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("publish %s: %w", currentFile, err)
	}
	syncDir(dir)
	return nil
}

// cleanupGenerations deletes generation dirs other than keep and prev, and
// files from the older flat layout once a generation has replaced them.
func cleanupGenerations(dir, keep, prev string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	legacy := map[string]bool{postingsFile: true, dictionaryFile: true, docsFile: true, contentsFile: true}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		switch {
		case e.IsDir() && strings.HasPrefix(e.Name(), genPrefix):
			if path != keep && path != prev {
				os.RemoveAll(path)
			}
		case !e.IsDir() && legacy[e.Name()] && filepath.Clean(prev) != filepath.Clean(dir):
			os.Remove(path)
		}
	}
}

// writeFileSync creates path, lets fn write through a buffer, then flushes,
// fsyncs and closes the file.
func writeFileSync(path string, fn func(w io.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", filepath.Base(path), err)
	}
	bw := bufio.NewWriterSize(f, 256*1024)
	if err := fn(bw); err != nil {
		f.Close()
		return err
	}
	if err := bw.Flush(); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync %s: %w", filepath.Base(path), err)
	}
	return f.Close()
}

func writeJSONSync(path string, v any) error {
	return writeFileSync(path, func(w io.Writer) error {
		if err := json.NewEncoder(w).Encode(v); err != nil {
			return fmt.Errorf("encode %s: %w", filepath.Base(path), err)
		}
		return nil
	})
}

// syncDir fsyncs a directory so renames inside it are durable. Best effort:
// some filesystems (e.g. SMB mounts) do not support it.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}
