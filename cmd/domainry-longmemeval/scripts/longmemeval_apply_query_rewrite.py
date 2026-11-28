#!/usr/bin/env python3
"""Attach a gold-isolated online query expansion to an existing semantic cache."""

import argparse
import hashlib
import json
from pathlib import Path


def sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--semantic-cache", required=True)
    parser.add_argument("--query-rewrite-cache", required=True)
    parser.add_argument("--output", required=True)
    return parser.parse_args()


def main():
    args = parse_args()
    semantic_bytes = Path(args.semantic_cache).read_bytes()
    rewrite_bytes = Path(args.query_rewrite_cache).read_bytes()
    semantic = json.loads(semantic_bytes)
    rewrite = json.loads(rewrite_bytes)
    semantic_cases = semantic.get("cases", [])
    rewrite_cases = rewrite.get("cases", [])
    if (
        semantic.get("format") != "domainry-longmemeval-semantic-v1"
        or rewrite.get("format") != "domainry-longmemeval-query-rewrite-v1"
        or semantic.get("dataset_sha256") != rewrite.get("dataset_sha256")
        or len(semantic_cases) != len(rewrite_cases)
    ):
        raise ValueError("semantic and query-rewrite caches do not match")
    for semantic_case, rewrite_case in zip(semantic_cases, rewrite_cases):
        if semantic_case.get("question_sha256") != rewrite_case.get("question_sha256"):
            raise ValueError("semantic and query-rewrite cache cases do not match")
        query = str(rewrite_case.get("query", ""))
        if not query or len(query.encode("utf-8")) > 1024:
            raise ValueError("query rewrite is empty or too large")
        semantic_case["retrieval_query"] = query
    semantic["query_rewrite_cache_sha256"] = sha256_bytes(rewrite_bytes)
    semantic["query_rewrite_model"] = str(rewrite.get("model", ""))
    semantic["query_rewrite_revision"] = str(rewrite.get("model_revision", ""))
    semantic["query_rewrite_request_mode"] = str(rewrite.get("request_mode", ""))
    if not all(
        (
            semantic["query_rewrite_model"],
            semantic["query_rewrite_revision"],
            semantic["query_rewrite_request_mode"],
        )
    ):
        raise ValueError("query-rewrite provenance is incomplete")
    source_hashes = list(semantic.get("source_cache_sha256", []))
    source_hashes.append(sha256_bytes(semantic_bytes))
    semantic["source_cache_sha256"] = sorted(set(source_hashes))
    Path(args.output).write_text(json.dumps(semantic, ensure_ascii=False, separators=(",", ":")), encoding="utf-8")


if __name__ == "__main__":
    main()
