#!/usr/bin/env python3
"""Clean, filter, and deduplicate a JSONL corpus.

Reads JSONL (one JSON object per line with a `text` field), normalizes text,
optionally detects language, filters by word count, and removes exact duplicates.

Usage:
  python3 scripts/clean_and_dedup.py --input wiki_abstracts.jsonl --output wiki_clean.jsonl
"""
import argparse
import json
import unicodedata
import re
import hashlib
from pathlib import Path

try:
    from langdetect import detect
except Exception:
    detect = None


def normalize_text(s: str) -> str:
    s = unicodedata.normalize('NFC', s)
    s = re.sub(r"[\r\t]+", " ", s)
    s = re.sub(r"\s+", " ", s)
    s = s.strip()
    return s


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--input", required=True)
    p.add_argument("--output", required=True)
    p.add_argument("--min-words", type=int, default=20)
    p.add_argument("--max-words", type=int, default=500)
    p.add_argument("--require-lang", default=None, help="e.g. en")
    args = p.parse_args()

    in_path = Path(args.input)
    out_f = open(args.output, "w", encoding="utf-8")

    seen = set()
    total = 0
    kept = 0

    with in_path.open(encoding="utf-8") as fh:
        for line in fh:
            total += 1
            try:
                obj = json.loads(line)
            except Exception:
                continue
            text = obj.get('text') or obj.get('content') or ''
            if not text:
                continue
            text = normalize_text(text)
            wc = len(text.split())
            if wc < args.min_words or wc > args.max_words:
                continue
            if args.require_lang and detect:
                try:
                    lang = detect(text)
                except Exception:
                    continue
                if lang != args.require_lang:
                    continue
            key = hashlib.sha256(text.encode('utf-8')).hexdigest()
            if key in seen:
                continue
            seen.add(key)
            obj['text'] = text
            out_f.write(json.dumps(obj, ensure_ascii=False) + "\n")
            kept += 1

    out_f.close()
    print(f"Read {total} lines, wrote {kept} cleaned docs to {args.output}")


if __name__ == '__main__':
    main()
