package index

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestLoadIndexRestoresSearch simulates a server restart: save, load into a
// fresh Index, and check search results and snippets are identical.
func TestLoadIndexRestoresSearch(t *testing.T) {
	idx := buildTestIndex()
	dir := t.TempDir()
	if err := SaveIndex(idx, dir); err != nil {
		t.Fatalf("SaveIndex: %v", err)
	}

	loaded, err := LoadIndex(dir)
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if loaded.Stats() != idx.Stats() {
		t.Fatalf("stats differ: loaded %+v, original %+v", loaded.Stats(), idx.Stats())
	}
	if loaded.Dirty() {
		t.Error("freshly loaded index should not be dirty")
	}

	for _, q := range []string{"Go programming", "rust", "concurrency goroutines"} {
		want := idx.Search(q, 10, "or")
		got := loaded.Search(q, 10, "or")
		if len(got.Results) != len(want.Results) {
			t.Fatalf("%q: %d results after load, want %d", q, len(got.Results), len(want.Results))
		}
		for i := range want.Results {
			w, g := want.Results[i], got.Results[i]
			if g.URL != w.URL || g.Score != w.Score || g.Snippet != w.Snippet {
				t.Errorf("%q result %d differs:\n got  %+v\n want %+v", q, i, g, w)
			}
		}
	}
}

func TestLoadIndexEmptyDir(t *testing.T) {
	_, err := LoadIndex(t.TempDir())
	if !errors.Is(err, ErrNoIndex) {
		t.Fatalf("expected ErrNoIndex, got %v", err)
	}
}

// TestSecondCrawlKeepsContent is a regression test: after the first save,
// documents from a later crawl used to read the wrong content, and the next
// save dropped the content of already-saved documents.
func TestSecondCrawlKeepsContent(t *testing.T) {
	dir := t.TempDir()
	idx := NewIndex()
	idx.AddDocument("a", "https://a.test", "A", "alpha content about apples")
	idx.AddDocument("b", "https://b.test", "B", "bravo content about bananas")
	if err := SaveIndex(idx, dir); err != nil {
		t.Fatalf("first save: %v", err)
	}

	// Second crawl.
	idx.AddDocument("c", "https://c.test", "C", "charlie content about cherries")
	want := map[uint32]string{
		0: "alpha content about apples",
		1: "bravo content about bananas",
		2: "charlie content about cherries",
	}
	for id, w := range want {
		if got := idx.GetDocContent(id); got != w {
			t.Errorf("before second save, doc %d = %q, want %q", id, got, w)
		}
	}

	if err := SaveIndex(idx, dir); err != nil {
		t.Fatalf("second save: %v", err)
	}
	for id, w := range want {
		if got := idx.GetDocContent(id); got != w {
			t.Errorf("after second save, doc %d = %q, want %q", id, got, w)
		}
	}

	// And after a restart.
	loaded, err := LoadIndex(dir)
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	for id, w := range want {
		if got := loaded.GetDocContent(id); got != w {
			t.Errorf("after reload, doc %d = %q, want %q", id, got, w)
		}
	}
	if r := loaded.Search("cherries", 10, "or"); len(r.Results) != 1 || r.Results[0].URL != "https://c.test" {
		t.Errorf("search after reload: %+v", r.Results)
	}
}

// TestLoadThenCrawlThenSave covers the restart path: load from disk, add
// documents, save again.
func TestLoadThenCrawlThenSave(t *testing.T) {
	dir := t.TempDir()
	idx := NewIndex()
	idx.AddDocument("a", "https://a.test", "A", "first document text")
	if err := SaveIndex(idx, dir); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded.AddDocument("b", "https://b.test", "B", "second document text")
	if !loaded.Dirty() {
		t.Error("index should be dirty after AddDocument")
	}
	if err := SaveIndex(loaded, dir); err != nil {
		t.Fatal(err)
	}

	again, err := LoadIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.Stats().DocCount != 2 {
		t.Fatalf("expected 2 docs, got %d", again.Stats().DocCount)
	}
	if got := again.GetDocContent(0); got != "first document text" {
		t.Errorf("doc 0 = %q", got)
	}
	if got := again.GetDocContent(1); got != "second document text" {
		t.Errorf("doc 1 = %q", got)
	}
	if r := again.Search("document", 10, "and"); r.TotalHits != 2 {
		t.Errorf("expected 2 hits, got %d", r.TotalHits)
	}
}

// TestSaveKeepsOnlyTwoGenerations checks old generations are cleaned up while
// the previous one is kept as a fallback.
func TestSaveKeepsOnlyTwoGenerations(t *testing.T) {
	dir := t.TempDir()
	idx := NewIndex()
	for i := 0; i < 4; i++ {
		idx.AddDocument(fmt.Sprint(i), fmt.Sprintf("https://%d.test", i), "t", "some text")
		if err := SaveIndex(idx, dir); err != nil {
			t.Fatal(err)
		}
	}

	var gens []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), genPrefix) {
			gens = append(gens, e.Name())
		}
	}
	if len(gens) != 2 || gens[0] != "gen-000003" || gens[1] != "gen-000004" {
		t.Errorf("expected [gen-000003 gen-000004], got %v", gens)
	}
	cur, _ := os.ReadFile(filepath.Join(dir, currentFile))
	if strings.TrimSpace(string(cur)) != "gen-000004" {
		t.Errorf("CURRENT = %q", cur)
	}
}

// TestCrashedSaveLeavesIndexIntact simulates a crash mid-save: a half-written
// generation dir exists but CURRENT was never switched to it.
func TestCrashedSaveLeavesIndexIntact(t *testing.T) {
	dir := t.TempDir()
	idx := NewIndex()
	idx.AddDocument("a", "https://a.test", "A", "durable text")
	if err := SaveIndex(idx, dir); err != nil {
		t.Fatal(err)
	}

	// Half-written next generation.
	broken := filepath.Join(dir, "gen-000002")
	os.MkdirAll(broken, 0755)
	os.WriteFile(filepath.Join(broken, dictionaryFile), []byte(`{"meta":`), 0644)

	loaded, err := LoadIndex(dir)
	if err != nil {
		t.Fatalf("LoadIndex should ignore unpublished generation: %v", err)
	}
	if got := loaded.GetDocContent(0); got != "durable text" {
		t.Errorf("content = %q", got)
	}

	// Next save skips past the leftover and cleans it up.
	loaded.AddDocument("b", "https://b.test", "B", "more text")
	if err := SaveIndex(loaded, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(broken); !os.IsNotExist(err) {
		t.Errorf("leftover generation should be removed, stat err = %v", err)
	}
}

// TestLoadLegacyFlatLayout loads an index saved before generations existed
// (files directly in the index dir) and migrates it on the next save.
func TestLoadLegacyFlatLayout(t *testing.T) {
	dir := t.TempDir()
	idx := NewIndex()
	idx.AddDocument("a", "https://a.test", "A", "legacy text")
	if err := SaveIndex(idx, dir); err != nil {
		t.Fatal(err)
	}
	// Flatten: move files to dir root, remove CURRENT and the generation dir.
	genDir, _ := resolveIndexDir(dir)
	for _, f := range []string{postingsFile, dictionaryFile, docsFile, contentsFile} {
		if err := os.Rename(filepath.Join(genDir, f), filepath.Join(dir, f)); err != nil {
			t.Fatal(err)
		}
	}
	os.Remove(filepath.Join(dir, currentFile))
	os.RemoveAll(genDir)

	loaded, err := LoadIndex(dir)
	if err != nil {
		t.Fatalf("LoadIndex legacy: %v", err)
	}
	if got := loaded.GetDocContent(0); got != "legacy text" {
		t.Errorf("content = %q", got)
	}

	loaded.AddDocument("b", "https://b.test", "B", "new text")
	if err := SaveIndex(loaded, dir); err != nil {
		t.Fatal(err)
	}
	if got := loaded.GetDocContent(0); got != "legacy text" {
		t.Errorf("legacy content lost after migration: %q", got)
	}
	// One more save drops the legacy files (they are no longer the fallback).
	if err := SaveIndex(loaded, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, postingsFile)); !os.IsNotExist(err) {
		t.Errorf("legacy postings.bin should be removed, stat err = %v", err)
	}
}

// TestSaveDuringConcurrentAdds checks documents added while a save runs are
// neither lost nor half-persisted. Run with -race.
func TestSaveDuringConcurrentAdds(t *testing.T) {
	dir := t.TempDir()
	idx := NewIndex()
	for i := 0; i < 50; i++ {
		idx.AddDocument(fmt.Sprint(i), fmt.Sprintf("https://%d.test", i), "t", fmt.Sprintf("doc number %d shared", i))
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 50; i < 200; i++ {
			idx.AddDocument(fmt.Sprint(i), fmt.Sprintf("https://%d.test", i), "t", fmt.Sprintf("doc number %d shared", i))
		}
	}()
	if err := SaveIndex(idx, dir); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if err := SaveIndex(idx, dir); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Stats() != idx.Stats() {
		t.Fatalf("stats differ: loaded %+v, original %+v", loaded.Stats(), idx.Stats())
	}
	for i := 0; i < 200; i++ {
		want := fmt.Sprintf("doc number %d shared", i)
		if got := loaded.GetDocContent(uint32(i)); got != want {
			t.Fatalf("doc %d = %q, want %q", i, got, want)
		}
	}
}

func TestSnippetEscapesHTML(t *testing.T) {
	content := `Learn golang <script>alert("x")</script> & more`
	got := GenerateSnippet(content, []string{"golang", "script"}, 200)
	if strings.Contains(got, "<script>") {
		t.Fatalf("snippet contains raw <script>: %s", got)
	}
	want := `Learn <mark>golang</mark> &lt;<mark>script</mark>&gt;alert(&#34;x&#34;)&lt;/<mark>script</mark>&gt; &amp; more`
	if got != want {
		t.Errorf("snippet:\n got  %s\n want %s", got, want)
	}
}

func TestSnippetTermMarkDoesNotBreakTags(t *testing.T) {
	got := GenerateSnippet("mark the spot", []string{"spot", "mark"}, 200)
	want := "<mark>mark</mark> the <mark>spot</mark>"
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
