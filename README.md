# go-search

A from-scratch web search engine written in Go. It crawls web pages, builds an in-memory inverted index with BM25 ranking, and exposes a JSON HTTP API for crawling, searching, and evaluation.

## Overview

The project has three main pieces:

- **Crawler** — powered by [`gocrawl`](https://github.com/Saurabh-1785/gocrawl), it fetches pages concurrently and extracts text content.
- **Inverted Index** — maps stemmed tokens to posting lists. Scoring uses BM25 (k1=1.2, b=0.75). The index is thread-safe and backed by an LRU result cache.
- **HTTP API** — eight endpoints for crawl management, search, evaluation, and observability.

Index data is persisted to disk after each crawl across four binary/JSON files in `data/index/`. On restart the files remain on disk; the in-memory index is rebuilt by re-crawling or the files can be loaded back directly.

---

## Requirements

- Go 1.24 or later
- No external runtime dependencies — the binary is fully self-contained

---

## Running locally

```bash
git clone https://github.com/Saurabh-1785/go-search
cd go-search

go run .
```

The server starts on port `8080` by default. Set the `PORT` environment variable to change it.

```bash
PORT=9090 go run .
```

---

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | HTTP listen port |
| `DATA_DIR` | `data` | Root directory for persistent index files. Set this to an Azure File Share mount path in production. |

---

## API reference

### Health

**GET /health**

Returns `200 OK` when the server is running.

```bash
curl http://localhost:8080/health
```

```json
{ "status": "ok", "service": "go-search" }
```

---

### Crawl

**POST /crawl**

Starts a background crawl job. Returns immediately with a `job_id`.

```bash
curl -X POST http://localhost:8080/crawl \
  -H 'Content-Type: application/json' \
  -d '{
    "url": "https://go.dev",
    "depth": 2,
    "max_pages": 50,
    "workers": 4,
    "output_path": "data/corpus.jsonl"
  }'
```

| Field | Type | Required | Description |
|---|---|---|---|
| `url` | string | yes | Seed URL to start crawling from |
| `depth` | int | yes | Maximum link-follow depth |
| `max_pages` | int | yes | Hard cap on total pages fetched |
| `workers` | int | yes | Number of concurrent crawler goroutines |
| `output_path` | string | no | Path to write crawled documents as JSONL |

```json
{ "job_id": "a3f1bc2d9e4f0012", "status": "running" }
```

When the crawl completes, the index is saved to `$DATA_DIR/index/` automatically.

---

**GET /crawl/status/{job_id}**

Returns the current state of a crawl job.

```bash
curl http://localhost:8080/crawl/status/a3f1bc2d9e4f0012
```

```json
{
  "job_id": "a3f1bc2d9e4f0012",
  "status": "completed",
  "seed_url": "https://go.dev",
  "started_at": "2026-06-30T10:00:00Z",
  "result": {
    "documents_written": 42,
    "documents_skipped": 3,
    "pages_visited": 45,
    "pages_failed": 2,
    "duration": "8.312s",
    "output_path": "data/corpus.jsonl"
  }
}
```

Status values: `pending`, `running`, `completed`, `failed`.

---

**GET /crawl/jobs**

Lists all crawl jobs (including running and completed).

```bash
curl http://localhost:8080/crawl/jobs
```

---

### Search

**GET /search**

Runs a BM25-ranked search against the in-memory index.

```bash
curl "http://localhost:8080/search?q=goroutine+scheduler&k=5&mode=or"
```

| Parameter | Default | Description |
|---|---|---|
| `q` | — | Search query (required) |
| `k` | `10` | Number of results to return |
| `mode` | `or` | `or` returns docs matching any term; `and` requires all terms |

```json
{
  "query": "goroutine scheduler",
  "results": [
    {
      "doc_id": 7,
      "url": "https://go.dev/doc/faq",
      "title": "Frequently Asked Questions",
      "score": 3.812,
      "snippet": "...the <mark>goroutine</mark> <mark>scheduler</mark> maps goroutines to OS threads..."
    }
  ],
  "total_hits": 12,
  "time_taken_ms": 0.84
}
```

Snippets include `<mark>` tags around matched terms. Results are cached with a 128-entry LRU cache; the cache is invalidated when new documents are indexed.

---

**GET /search/stats**

Returns query log statistics: total queries, average latency, QPS, and the last 100 queries.

```bash
curl http://localhost:8080/search/stats
```

```json
{
  "total_queries": 143,
  "avg_latency_ms": 0.72,
  "qps": 2.1,
  "uptime_seconds": 68.4,
  "recent": [ ... ]
}
```

---

### Index

**GET /index/stats**

Returns counts of terms, documents, and total postings in the index.

```bash
curl http://localhost:8080/index/stats
```

```json
{
  "term_count": 18432,
  "doc_count": 42,
  "total_postings": 294812
}
```

---

### Evaluation

**POST /eval**

Runs precision@K and recall evaluation against human relevance judgments.

```bash
curl -X POST http://localhost:8080/eval \
  -H 'Content-Type: application/json' \
  -d '{
    "k": 10,
    "judgments": [
      {
        "query": "goroutine scheduler",
        "relevant_urls": [
          "https://go.dev/doc/faq",
          "https://go.dev/blog/scheduler"
        ]
      }
    ]
  }'
```

```json
{
  "results": [
    {
      "query": "goroutine scheduler",
      "precision_at_k": 0.2,
      "recall": 1.0,
      "k": 10,
      "hits": 2,
      "total_relevant": 2
    }
  ],
  "mean_precision_at_k": 0.2,
  "mean_recall": 1.0,
  "queries_run": 1
}
```

---

## How the index works

### Tokenization pipeline

All text goes through a four-step pipeline before being stored or queried:

1. Lowercase the entire input
2. Split on any non-letter, non-digit Unicode character
3. Remove stopwords
4. Apply Porter stemming

The same pipeline is applied to both indexed documents and incoming queries, so `"running"` and `"runs"` both match the stem `"run"`.

### BM25 scoring

Each query term is scored per document using:

```
score = IDF × (tf × (k1 + 1)) / (tf + k1 × (1 - b + b × |D| / avgdl))
```

with default parameters `k1 = 1.2` and `b = 0.75`. In `or` mode all matching terms contribute to the score. In `and` mode only documents containing every query term are considered.

### Persistence format

After each crawl, `SaveIndex` writes four files to `$DATA_DIR/index/`:

| File | Format | Contents |
|---|---|---|
| `dictionary.json` | JSON | Term → `{offset, length, df}` entries pointing into `postings.bin` |
| `postings.bin` | Binary | Delta + varint-encoded posting lists (DocID, TF) |
| `docs.json` | JSON | Document metadata table: URL, title, content offset/length |
| `contents.bin` | Binary | Raw page text, length-prefixed per document |

Postings are loaded lazily — only the posting list for terms actually queried are read from disk, not the entire file.

---

## Project structure

```
.
├── main.go               — server startup, route registration
├── crawl_handler.go      — POST /crawl, GET /crawl/status/:id, GET /crawl/jobs
├── crawl_job.go          — JobManager, background crawl goroutines
├── search_handler.go     — GET /search, GET /search/stats, POST /eval
├── index/
│   ├── index.go          — in-memory inverted index, AddDocument, GetPostings
│   ├── bm25.go           — BM25 IDF and per-term scoring
│   ├── search.go         — multi-term query execution, result ranking
│   ├── snippet.go        — context snippet extraction with <mark> highlighting
│   ├── persist.go        — SaveIndex / LoadDictionary / LoadDocTable
│   ├── compress.go       — delta + varint encoding for posting lists
│   ├── cache.go          — LRU search result cache
│   ├── qlog.go           — per-query latency and QPS tracking
│   ├── metrics.go        — Precision@K and recall evaluation
│   └── tfidf.go          — TF-IDF helpers (used internally)
├── tokenizer/
│   ├── tokenizer.go      — full pipeline: normalize → split → stopwords → stem
│   └── ...               — stopword list, Porter stemmer
├── data/
│   └── index/            — persisted index files (gitignored)
└── deploy/
    ├── azure-setup.sh    — provision Azure resources and deploy
    ├── update.sh         — rebuild and redeploy after code changes
    └── README.md         — step-by-step Azure deployment guide
```

---

## Deploying to Azure

The application is containerized and designed to run on Azure Container Apps with index data persisted to an Azure File Share so it survives container restarts.

See [deploy/README.md](deploy/README.md) for the complete guide.

Quick summary:

```bash
# 1. Install az CLI and log in
curl -sL https://aka.ms/InstallAzureCLIDeb | sudo bash
az login

# 2. Register Azure providers (one-time)
az provider register --namespace Microsoft.App
az provider register --namespace Microsoft.OperationalInsights

# 3. Run setup (set DOCKER_USER to your Docker Hub username)
chmod +x deploy/azure-setup.sh
DOCKER_USER=yourname ./deploy/azure-setup.sh
```

The script provisions a Standard LRS Storage Account, a 5 GiB File Share, and a Container App with the share mounted at `/mnt/data`. The `DATA_DIR=/mnt/data` environment variable is set automatically.

To redeploy after a code change:

```bash
DOCKER_USER=yourname ./deploy/update.sh
```

---

## Running tests

```bash
go test ./...

# With verbose output and race detector
go test -race -v ./...

# Run benchmarks
go test -bench=. -benchmem ./index/...
```

---

## Dependencies

| Package | Purpose |
|---|---|
| [`github.com/Saurabh-1785/gocrawl`](https://github.com/Saurabh-1785/gocrawl) | Concurrent web crawler and HTML content extractor |
| `golang.org/x/net` | HTML parsing (transitive dependency of gocrawl) |

No other third-party libraries are used. The inverted index, BM25 scorer, tokenizer, LRU cache, and persistence layer are all implemented from scratch.

---

## License

MIT — see [LICENSE](LICENSE).
