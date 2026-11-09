# go-search

A from-scratch web search engine written in Go. It crawls web pages, builds an in-memory inverted index with BM25 ranking, and exposes a JSON HTTP API for crawling, searching, and evaluation.

## Overview

The project has three main pieces:

- **Crawler** — powered by [`gocrawl`](https://github.com/Saurabh-1785/gocrawl), it fetches pages concurrently and extracts text content.
- **Inverted Index** — maps stemmed tokens to posting lists. Scoring uses BM25 (k1=1.2, b=0.75). The index is thread-safe and backed by an LRU result cache.
- **HTTP API** — eight endpoints for crawl management, search, evaluation, and observability. Search is public; crawl, eval and stats need an admin API key.

The index is saved to `$DATA_DIR/index/` after each crawl and on shutdown, and loaded back on startup, so it survives restarts. Saves are atomic: a crash mid-save leaves the previous index intact.

---

## Requirements

- Go 1.24 or later
- No external runtime dependencies — the binary is fully self-contained

---

## Running locally

```bash
git clone https://github.com/Saurabh-1785/go-search
cd go-search

go run ./cmd/gosearch
```

The server starts on port `8080` by default. Set the `PORT` environment variable to change it.

```bash
PORT=9090 go run ./cmd/gosearch
```

---

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | HTTP listen port |
| `DATA_DIR` | `data` | Root directory for persistent index files. Set this to an Azure File Share mount path in production. |
| `ADMIN_API_KEY` | random per run | Key for admin endpoints, at least 16 characters. If unset, a key is generated at startup and printed in the log. |
| `TRUST_PROXY_HEADERS` | `false` | Use the rightmost `X-Forwarded-For` entry as the client IP for rate limiting. Enable only behind an ingress that sets it (Azure Container Apps does), or clients can forge it. |
| `ALLOW_PRIVATE_TARGETS` | `false` | Turn off SSRF protection so you can crawl `localhost` during development. **Never enable in production.** |

Local development with a site on localhost:

```bash
ADMIN_API_KEY=dev-key-0123456789 ALLOW_PRIVATE_TARGETS=true go run ./cmd/gosearch
```

---

## API reference

Endpoints marked **admin** require `Authorization: Bearer <ADMIN_API_KEY>`. All endpoints except `/health` are rate limited per client IP and return `429` with a `Retry-After` header when exceeded:

| Group | Limit |
|---|---|
| `/search`, `/index/stats` | 120/min, burst 30 |
| Admin endpoints (counted before the key check) | 30/min, burst 10 |
| Starting a crawl (in addition) | 10/hour, burst 3, max 2 running at once |

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

**POST /crawl** — admin

Starts a background crawl job. Returns immediately with a `job_id`.

```bash
curl -X POST http://localhost:8080/crawl \
  -H "Authorization: Bearer $ADMIN_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "url": "https://go.dev",
    "depth": 2,
    "max_pages": 50,
    "workers": 4
  }'
```

| Field | Type | Required | Description |
|---|---|---|---|
| `url` | string | yes | Seed URL (`http`/`https`, public address only) |
| `depth` | int | no | Link-follow depth, 1–3 (default 2) |
| `max_pages` | int | no | Cap on pages fetched, 1–500 (default 50) |
| `workers` | int | no | Concurrent crawler goroutines, 1–8 (default 3) |

Unknown fields are rejected. The former `output_path` field was removed: it let callers choose a file path for the server to write to.

```json
{ "job_id": "a3f1bc2d9e4f0012", "status": "running" }
```

When the crawl finishes (or fails part-way), the index is saved to `$DATA_DIR/index/`.

---

**GET /crawl/status/{job_id}** — admin

Returns the current state of a crawl job.

```bash
curl -H "Authorization: Bearer $ADMIN_API_KEY" http://localhost:8080/crawl/status/a3f1bc2d9e4f0012
```

```json
{
  "job_id": "a3f1bc2d9e4f0012",
  "status": "completed",
  "seed_url": "https://go.dev",
  "started_at": "2026-06-30T10:00:00Z",
  "finished_at": "2026-06-30T10:00:08Z",
  "result": {
    "documents_indexed": 43,
    "pages_visited": 45,
    "pages_failed": 2,
    "duration": "8.312s"
  }
}
```

Status values: `running`, `completed`, `failed`. The last 100 finished jobs are kept.

---

**GET /crawl/jobs** — admin

Lists crawl jobs, newest first.

```bash
curl -H "Authorization: Bearer $ADMIN_API_KEY" http://localhost:8080/crawl/jobs
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
| `q` | — | Search query (required, max 512 bytes and 32 terms) |
| `k` | `10` | Number of results to return (1–100) |
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

Snippets are HTML-escaped page text with `<mark>` tags around matched terms, so they are safe to render as HTML. `title` and `url` are plain text: render them as text, not HTML. Results are cached with a 128-entry LRU cache; the cache is invalidated when new documents are indexed.

---

**GET /search/stats** — admin

Returns query log statistics: total queries, average latency, QPS, and latency/result counts of the last 100 queries. Query text is never stored or logged, since searches can contain personal data.

```bash
curl -H "Authorization: Bearer $ADMIN_API_KEY" http://localhost:8080/search/stats
```

```json
{
  "total_queries": 143,
  "avg_latency_ms": 0.72,
  "qps": 2.1,
  "uptime_seconds": 68.4,
  "recent": [ { "latency_ns": 35294, "results": 1, "time": "2026-06-30T10:01:00Z" } ]
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

**POST /eval** — admin

Runs precision@K and recall evaluation against human relevance judgments (max 100 judgments, `k` 1–100).

```bash
curl -X POST http://localhost:8080/eval \
  -H "Authorization: Bearer $ADMIN_API_KEY" \
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

After each crawl and on shutdown (`SIGTERM`/`SIGINT`), `SaveIndex` writes a new *generation* directory, then atomically switches a `CURRENT` pointer to it:

```
$DATA_DIR/index/
├── CURRENT            — "gen-000004"
├── gen-000003/        — previous generation, kept as a fallback
└── gen-000004/        — active generation
```

A crash mid-save leaves `CURRENT` pointing at the previous complete generation. Searches keep running during a save: the index is snapshotted under a read lock and files are written without holding it. On startup, `LoadIndex` reads the active generation back into memory (indexes from before generations existed, with files directly in `index/`, still load). If the index is present but corrupt, the server refuses to start rather than overwrite it.

Each generation holds four files:

| File | Format | Contents |
|---|---|---|
| `dictionary.json` | JSON | Term → `{offset, length, df}` entries pointing into `postings.bin` |
| `postings.bin` | Binary | Delta + varint-encoded posting lists (DocID, TF) |
| `docs.json` | JSON | Document metadata table: URL, title, content offset/length |
| `contents.bin` | Binary | Raw page text, length-prefixed per document |

Posting lists are loaded into memory at startup. Page text stays on disk; only the top-K results' text is read, to build snippets.

---

## Security

| Threat | Mitigation |
|---|---|
| SSRF (crawling `169.254.169.254`, localhost, private networks) | Seed URL validated, then every connection — redirects, followed links, DNS rebinding — checked at dial time against private, loopback, link-local, CGNAT and reserved ranges, plus Azure WireServer `168.63.129.16`. Proxies disabled for crawler traffic. See `internal/netguard`. |
| Unauthenticated crawl / eval / stats | Bearer API key, compared in constant time. |
| Abuse and brute force | Per-IP token-bucket rate limits (IPv6 grouped by /64); admin limits apply before the key check. Max 2 concurrent crawls. |
| Arbitrary file write | `output_path` removed; unknown JSON fields rejected. |
| Resource exhaustion | Body size limits, caps on `k`, query length/terms, eval size and crawl depth/pages/workers; server read/write/idle timeouts (Slowloris). |
| Query privacy | Query text is not stored in stats or written to logs. |
| XSS via crawled content | Snippets HTML-escaped before `<mark>` tags are added; JSON API sends `nosniff`, `X-Frame-Options: DENY` and a restrictive CSP. |
| Data loss / corruption | Atomic generation-based saves, save on shutdown, load on startup. |

Rate limits, the job list and the index live in process memory, so the app is meant to run as a **single replica** (`maxReplicas: 1` in the Azure setup).

---

## Project structure

Standard Go layout: `cmd/` holds the entrypoint, `internal/` holds one package per job (the Go compiler stops other modules importing it).

```
.
├── cmd/
│   └── gosearch/
│       └── main.go           — entrypoint: load config + index, start server, graceful shutdown
├── internal/
│   ├── config/               — environment variables → Config
│   ├── server/               — HTTP layer
│   │   ├── server.go         — Server struct, route table, SaveIndex
│   │   ├── crawl.go          — POST /crawl, GET /crawl/status/:id, GET /crawl/jobs
│   │   ├── search.go         — GET /search, GET /search/stats, POST /eval, input limits
│   │   ├── middleware.go     — admin auth, rate-limit wrapper, body limits, security headers, request log
│   │   └── respond.go        — JSON encode/decode helpers
│   ├── crawljob/             — background crawl jobs: Manager, request limits, job history
│   ├── netguard/             — SSRF guard: dial-time IP filtering, seed URL validation
│   ├── ratelimit/            — per-client token-bucket limiter, client IP detection
│   ├── index/
│   │   ├── index.go          — in-memory inverted index, AddDocument, GetPostings
│   │   ├── bm25.go           — BM25 IDF and per-term scoring
│   │   ├── search.go         — multi-term query execution, result ranking
│   │   ├── snippet.go        — snippet extraction, HTML escaping, <mark> highlighting
│   │   ├── persist.go        — atomic SaveIndex, LoadIndex, generation management
│   │   ├── compress.go       — delta + varint encoding for posting lists
│   │   ├── cache.go          — LRU search result cache
│   │   ├── qlog.go           — per-query latency and QPS tracking (no query text)
│   │   ├── metrics.go        — Precision@K and recall evaluation
│   │   └── tfidf.go          — TF-IDF helpers (used internally)
│   └── tokenizer/
│       ├── tokenizer.go      — full pipeline: normalize → split → stopwords → stem
│       └── ...               — stopword list, Porter stemmer
├── deploy/
│   ├── azure.env             — shared names/settings (override via env vars)
│   ├── lib.sh                — shared helpers: preflight, image tags, waits
│   ├── azure-setup.sh        — create or update the whole stack (idempotent)
│   ├── update.sh             — rebuild and roll out a new revision
│   ├── azure-teardown.sh     — delete the app (keep data) or everything
│   └── README.md             — step-by-step Azure deployment guide
├── data/index/               — persisted index (gitignored)
├── docs/                     — personal notes (gitignored)
└── Dockerfile
```

Dependencies point one way: `cmd` → `server` → `crawljob`, `netguard`, `ratelimit`, `index` → `tokenizer`. The index package knows nothing about HTTP.

---

## Deploying to Azure

The app runs on Azure Container Apps, with the index on an Azure File Share so it survives restarts. Full guide: [deploy/README.md](deploy/README.md).

```bash
az login && docker login
DOCKER_USER=yourname ./deploy/azure-setup.sh   # create or update everything (safe to re-run)
DOCKER_USER=yourname ./deploy/update.sh        # after a code change: new image, new revision
./deploy/azure-teardown.sh                     # delete the app, keep the index
```

The setup script checks prerequisites first, keeps your existing admin key on re-runs, tags every image uniquely (`<UTC time>-<git sha>`), and waits until `/health` responds.

---

## Running tests

```bash
go test ./...

# With verbose output and race detector
go test -race -v ./...

# Run benchmarks
go test -bench=. -benchmem ./internal/index/
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
