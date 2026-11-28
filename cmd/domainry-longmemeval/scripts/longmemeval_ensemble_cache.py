#!/usr/bin/env python3
"""Fuse two gold-isolated LongMemEval semantic caches with fixed weights."""

import argparse
import hashlib
import json
from pathlib import Path


FORBIDDEN_FIELDS = {"answer", "answer_session_ids", "has_answer", "question_id", "question_type"}


def sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--left", required=True)
    parser.add_argument("--right", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--left-weight", type=float, default=0.5)
    return parser.parse_args()


def normalize(values):
    if not values:
        return []
    minimum, maximum = min(values), max(values)
    if minimum == maximum:
        return [0.0 for _ in values]
    return [(value - minimum) / (maximum - minimum) for value in values]


def main():
    args = parse_args()
    if not 0 <= args.left_weight <= 1:
        raise ValueError("left weight must be between zero and one")
    left_bytes = Path(args.left).read_bytes()
    right_bytes = Path(args.right).read_bytes()
    left, right = json.loads(left_bytes), json.loads(right_bytes)
    if (
        left.get("format") != "domainry-longmemeval-semantic-v1"
        or right.get("format") != "domainry-longmemeval-semantic-v1"
        or left.get("dataset_sha256") != right.get("dataset_sha256")
        or left.get("gold_fields_dropped_during_json_decode") != sorted(FORBIDDEN_FIELDS)
        or right.get("gold_fields_dropped_during_json_decode") != sorted(FORBIDDEN_FIELDS)
        or len(left.get("cases", [])) != len(right.get("cases", []))
    ):
        raise ValueError("semantic caches are incompatible")
    cases = []
    for left_case, right_case in zip(left["cases"], right["cases"]):
        if left_case["question_sha256"] != right_case["question_sha256"] or len(left_case["sessions"]) != len(right_case["sessions"]):
            raise ValueError("semantic cache cases do not match")
        left_session_scores = normalize([value["score"] for value in left_case["sessions"]])
        right_session_scores = normalize([value["score"] for value in right_case["sessions"]])
        sessions = []
        for index, (left_session, right_session) in enumerate(zip(left_case["sessions"], right_case["sessions"])):
            if left_session["content_sha256"] != right_session["content_sha256"] or len(left_session["turn_scores"]) != len(right_session["turn_scores"]):
                raise ValueError("semantic cache sessions do not match")
            left_turns = normalize(left_session["turn_scores"])
            right_turns = normalize(right_session["turn_scores"])
            turns = [args.left_weight * left_score + (1 - args.left_weight) * right_score for left_score, right_score in zip(left_turns, right_turns)]
            sessions.append(
                {
                    "content_sha256": left_session["content_sha256"],
                    "score": args.left_weight * left_session_scores[index] + (1 - args.left_weight) * right_session_scores[index],
                    "turn_scores": turns,
                }
            )
        case = {"question_sha256": left_case["question_sha256"], "sessions": sessions}
        retrieval_query = left_case.get("retrieval_query") or right_case.get("retrieval_query")
        if retrieval_query:
            case["retrieval_query"] = retrieval_query
        cases.append(case)
    output = {
        "format": "domainry-longmemeval-semantic-v1",
        "dataset_sha256": left["dataset_sha256"],
        "model": left["model"] + "+" + right["model"],
        "model_revision": left["model_revision"] + "+" + right["model_revision"],
        "query_prefix": f"weighted_ensemble_left_{args.left_weight:.4f}",
        "granularity": "candidate_sentence_ensemble",
        "prefilter_cache_sha256": left.get("prefilter_cache_sha256", ""),
        "prefilter_sessions": min(left.get("prefilter_sessions", 0), right.get("prefilter_sessions", 0)),
        "prefilter_turns": min(left.get("prefilter_turns", 0), right.get("prefilter_turns", 0)),
        "source_cache_sha256": [sha256_bytes(left_bytes), sha256_bytes(right_bytes)],
        "query_rewrite_cache_sha256": left.get("query_rewrite_cache_sha256", "") or right.get("query_rewrite_cache_sha256", ""),
        "query_rewrite_model": left.get("query_rewrite_model", "") or right.get("query_rewrite_model", ""),
        "query_rewrite_revision": left.get("query_rewrite_revision", "") or right.get("query_rewrite_revision", ""),
        "query_rewrite_request_mode": left.get("query_rewrite_request_mode", "") or right.get("query_rewrite_request_mode", ""),
        "gold_fields_dropped_during_json_decode": sorted(FORBIDDEN_FIELDS),
        "cases": cases,
    }
    Path(args.output).write_text(json.dumps(output, separators=(",", ":")), encoding="utf-8")


if __name__ == "__main__":
    main()
