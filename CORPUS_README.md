# Wikipedia abstracts corpus — quick guide

Steps:

1. Download the English Wikipedia dump and extract articles with WikiExtractor:

```bash
wget https://dumps.wikimedia.org/enwiki/latest/enwiki-latest-pages-articles.xml.bz2
git clone https://github.com/attardi/wikiextractor.git
python3 wikiextractor/WikiExtractor.py -o extracted enwiki-latest-pages-articles.xml.bz2
```

2. Extract first-paragraph abstracts to JSONL:

```bash
python3 scripts/wikiextract_abstracts.py --input-dir extracted --output wiki_abstracts.jsonl
```

3. Clean and deduplicate (optional language filter):

```bash
pip install -r requirements.txt
python3 scripts/clean_and_dedup.py --input wiki_abstracts.jsonl --output wiki_clean.jsonl --require-lang en
```

Output format: newline-delimited JSON objects with fields `id`, `title`, `text`, `source`, `lang`.

Notes:
- `WikiExtractor` must be run first; its output will be searched for `<doc ...>...</doc>` blocks.
- Tweak `--min-words` and `--max-words` flags on both scripts to control document length.
- For large corpora, process files in shards and gzip outputs.
