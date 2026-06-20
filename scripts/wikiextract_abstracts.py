#!/usr/bin/env python3
"""Read WikiExtractor output and write Wikipedia abstracts to JSONL.

Usage:
  python3 scripts/wikiextract_abstracts.py --input-dir extracted --output wiki_abstracts.jsonl
"""
import argparse
import json
import os
import re
from pathlib import Path
import hashlib


DOC_RE = re.compile(r'<doc([^>]*)>(.*?)</doc>', re.DOTALL)
TITLE_RE = re.compile(r'title="([^"]+)"')
ID_RE = re.compile(r'id="(\d+)"')


def first_paragraph(text: str) -> str:
    parts = [p.strip() for p in re.split(r"\n\s*\n", text) if p.strip()]
    return parts[0] if parts else ""


def iter_docs_from_file(path: Path):
    data = path.read_text(encoding="utf-8")
    for m in DOC_RE.finditer(data):
        attrs, body = m.groups()
        title_m = TITLE_RE.search(attrs)
        id_m = ID_RE.search(attrs)
        title = title_m.group(1) if title_m else None
        doc_id = id_m.group(1) if id_m else None
        text = body.strip()
        yield doc_id, title, text


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--input-dir", required=True)
    p.add_argument("--output", required=True)
    p.add_argument("--min-words", type=int, default=20)
    p.add_argument("--max-words", type=int, default=1000)
    args = p.parse_args()

    in_dir = Path(args.input_dir)
    out_f = open(args.output, "w", encoding="utf-8")

    seen_hashes = set()
    total = 0
    kept = 0

    for root, _, files in os.walk(in_dir):
        for fname in files:
            path = Path(root) / fname
            try:
                for doc_id, title, body in iter_docs_from_file(path):
                    total += 1
                    abstract = first_paragraph(body)
                    if not abstract:
                        continue
                    wcount = len(abstract.split())
                    if wcount < args.min_words or wcount > args.max_words:
                        continue
                    key = hashlib.sha256(abstract.encode('utf-8')).hexdigest()
                    if key in seen_hashes:
                        continue
                    seen_hashes.add(key)
                    obj = {
                        "id": doc_id or key,
                        "title": title or "",
                        "text": abstract,
                        "source": "wikipedia",
                        "lang": "en",
                    }
                    out_f.write(json.dumps(obj, ensure_ascii=False) + "\n")
                    kept += 1
            except Exception:
                # skip problematic files
                continue

    out_f.close()
    print(f"Processed {total} docs, wrote {kept} abstracts to {args.output}")


if __name__ == '__main__':
    main()
