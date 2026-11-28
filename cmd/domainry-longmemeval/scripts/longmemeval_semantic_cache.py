#!/usr/bin/env python3
"""Build an offline LongMemEval semantic cache from non-gold fields only."""

import argparse
import hashlib
import json
import re
from pathlib import Path

import numpy as np
from sentence_transformers import SentenceTransformer


FORBIDDEN_FIELDS = {
    "answer",
    "answer_session_ids",
    "has_answer",
    "question_id",
    "question_type",
}


def sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def public_object(pairs):
    return {key: value for key, value in pairs if key not in FORBIDDEN_FIELDS}


def session_hash(session_id: str, date: str, turns: list[dict]) -> str:
    digest = hashlib.sha256()
    for value in (session_id, date):
        digest.update(value.encode("utf-8"))
        digest.update(b"\0")
    for turn in turns:
        digest.update(str(turn.get("role", "")).encode("utf-8"))
        digest.update(b"\0")
        digest.update(str(turn.get("content", "")).encode("utf-8"))
        digest.update(b"\0")
    return digest.hexdigest()


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--model", default="sentence-transformers/all-MiniLM-L6-v2")
    parser.add_argument("--revision", required=True)
    parser.add_argument("--query-prefix", default="")
    parser.add_argument("--granularity", choices=("turn", "sentence"), default="turn")
    parser.add_argument("--prefilter-cache")
    parser.add_argument("--prefilter-sessions", type=int, default=20)
    parser.add_argument("--prefilter-turns", type=int, default=3)
    parser.add_argument("--query-rewrite-cache")
    parser.add_argument("--device", default="cpu")
    parser.add_argument("--batch-size", type=int, default=128)
    return parser.parse_args()


def main():
    args = parse_args()
    input_path = Path(args.input)
    dataset_bytes = input_path.read_bytes()
    dataset_sha256 = sha256_bytes(dataset_bytes)
    cases = json.loads(dataset_bytes, object_pairs_hook=public_object)
    prefilter = None
    prefilter_sha256 = ""
    if args.prefilter_cache:
        prefilter_bytes = Path(args.prefilter_cache).read_bytes()
        prefilter_sha256 = sha256_bytes(prefilter_bytes)
        prefilter = json.loads(prefilter_bytes)
        if prefilter.get("dataset_sha256") != dataset_sha256 or len(prefilter.get("cases", [])) != len(cases):
            raise ValueError("prefilter cache does not match the dataset")
    rewrites = None
    rewrite_sha256 = ""
    if args.query_rewrite_cache:
        rewrite_bytes = Path(args.query_rewrite_cache).read_bytes()
        rewrite_sha256 = sha256_bytes(rewrite_bytes)
        rewrites = json.loads(rewrite_bytes)
        if rewrites.get("format") != "domainry-longmemeval-query-rewrite-v1" or rewrites.get("dataset_sha256") != dataset_sha256 or len(rewrites.get("cases", [])) != len(cases):
            raise ValueError("query rewrite cache does not match the dataset")
    model = SentenceTransformer(args.model, revision=args.revision, device=args.device)
    texts = []
    layouts = []
    for case_index, case in enumerate(cases):
        question = str(case["question"])
        retrieval_query = question
        if rewrites is not None:
            rewrite = rewrites["cases"][case_index]
            if rewrite.get("question_sha256") != sha256_bytes(question.encode("utf-8")) or not str(rewrite.get("query", "")).strip():
                raise ValueError("query rewrite cache case does not match the dataset")
            retrieval_query = str(rewrite["query"])
        sessions = case["haystack_sessions"]
        selected_turns = None
        if prefilter is not None:
            cached_sessions = prefilter["cases"][case_index]["sessions"]
            selected_sessions = sorted(range(len(cached_sessions)), key=lambda value: cached_sessions[value]["score"], reverse=True)[: args.prefilter_sessions]
            selected_turns = {
                (session_index, turn_index)
                for session_index in selected_sessions
                for turn_index in sorted(
                    range(len(cached_sessions[session_index]["turn_scores"])),
                    key=lambda value: cached_sessions[session_index]["turn_scores"][value],
                    reverse=True,
                )[: args.prefilter_turns]
            }
        question_position = len(texts)
        texts.append(args.query_prefix + retrieval_query)
        session_turn_ranges = []
        for session_index, turns in enumerate(sessions):
            turn_ranges = []
            for turn_index, turn in enumerate(turns):
                if selected_turns is not None and (session_index, turn_index) not in selected_turns:
                    turn_ranges.append(None)
                    continue
                start = len(texts)
                role = str(turn.get("role", ""))
                content = str(turn.get("content", ""))
                fragments = [content]
                if args.granularity == "sentence":
                    fragments = [value.strip() for value in re.split(r"(?<=[.!?])\s+|\n+", content) if value.strip()]
                    if not fragments:
                        fragments = [content]
                texts.extend(f"{role}: {fragment}" for fragment in fragments)
                turn_ranges.append((start, len(texts)))
            session_turn_ranges.append(turn_ranges)
        layouts.append((question_position, session_turn_ranges))
    embeddings = model.encode(
        texts,
        batch_size=args.batch_size,
        convert_to_numpy=True,
        normalize_embeddings=True,
        show_progress_bar=True,
    )
    output_cases = []
    for index, case in enumerate(cases):
        question = str(case["question"])
        session_ids = case["haystack_session_ids"]
        dates = case["haystack_dates"]
        sessions = case["haystack_sessions"]
        question_position, session_turn_ranges = layouts[index]
        query_vector = embeddings[question_position]
        output_sessions = []
        for session_index, turns in enumerate(sessions):
            if prefilter is None:
                turn_scores = []
            else:
                turn_scores = list(prefilter["cases"][index]["sessions"][session_index]["turn_scores"])
            for turn_index, turn_range in enumerate(session_turn_ranges[session_index]):
                if turn_range is None:
                    continue
                start, end = turn_range
                fragment_scores = np.matmul(embeddings[start:end], query_vector).astype(float).tolist()
                score = max(fragment_scores, default=0.0)
                if prefilter is None:
                    turn_scores.append(score)
                else:
                    turn_scores[turn_index] = max(turn_scores[turn_index], score)
            output_sessions.append(
                {
                    "content_sha256": session_hash(session_ids[session_index], dates[session_index], turns),
                    "score": max(turn_scores, default=0.0),
                    "turn_scores": turn_scores,
                }
            )
        output_cases.append(
            {
                "question_sha256": sha256_bytes(question.encode("utf-8")),
                "sessions": output_sessions,
            }
        )
    output = {
        "format": "domainry-longmemeval-semantic-v1",
        "dataset_sha256": dataset_sha256,
        "model": args.model,
        "model_revision": args.revision,
        "query_prefix": args.query_prefix,
        "granularity": args.granularity,
        "prefilter_cache_sha256": prefilter_sha256,
        "prefilter_sessions": args.prefilter_sessions if prefilter is not None else 0,
        "prefilter_turns": args.prefilter_turns if prefilter is not None else 0,
        "query_rewrite_cache_sha256": rewrite_sha256,
        "device": args.device,
        "gold_fields_dropped_during_json_decode": sorted(FORBIDDEN_FIELDS),
        "cases": output_cases,
    }
    Path(args.output).write_text(json.dumps(output, separators=(",", ":")), encoding="utf-8")


if __name__ == "__main__":
    main()
