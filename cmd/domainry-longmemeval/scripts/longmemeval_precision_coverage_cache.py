#!/usr/bin/env python3
"""Keep one precision anchor, then fill coverage with rank fusion."""

import argparse
import hashlib
import json
from pathlib import Path


FORBIDDEN_FIELDS = {"answer", "answer_session_ids", "has_answer", "question_id", "question_type"}


def sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--precision-cache", required=True)
    parser.add_argument("--coverage-cache", required=True)
    parser.add_argument("--precision-weight", type=float, default=0.7)
    parser.add_argument("--strategy", choices=("rrf", "interleave"), default="rrf")
    parser.add_argument("--output", required=True)
    return parser.parse_args()


def ranks(scores):
    order = sorted(range(len(scores)), key=lambda index: (-scores[index], index))
    positions = [0] * len(scores)
    for position, index in enumerate(order, start=1):
        positions[index] = position
    return positions, order


def main():
    args = parse_args()
    if not 0 <= args.precision_weight <= 1:
        raise ValueError("precision weight must be between zero and one")
    precision_bytes = Path(args.precision_cache).read_bytes()
    coverage_bytes = Path(args.coverage_cache).read_bytes()
    precision = json.loads(precision_bytes)
    coverage = json.loads(coverage_bytes)
    if (
        precision.get("format") != "domainry-longmemeval-semantic-v1"
        or coverage.get("format") != "domainry-longmemeval-semantic-v1"
        or precision.get("dataset_sha256") != coverage.get("dataset_sha256")
        or precision.get("gold_fields_dropped_during_json_decode") != sorted(FORBIDDEN_FIELDS)
        or coverage.get("gold_fields_dropped_during_json_decode") != sorted(FORBIDDEN_FIELDS)
        or len(precision.get("cases", [])) != len(coverage.get("cases", []))
    ):
        raise ValueError("precision and coverage caches are incompatible")

    output_cases = []
    for precision_case, coverage_case in zip(precision["cases"], coverage["cases"]):
        if (
            precision_case.get("question_sha256") != coverage_case.get("question_sha256")
            or len(precision_case.get("sessions", [])) != len(coverage_case.get("sessions", []))
        ):
            raise ValueError("precision and coverage cache cases do not match")
        precision_scores = [session["score"] for session in precision_case["sessions"]]
        coverage_scores = [session["score"] for session in coverage_case["sessions"]]
        precision_ranks, precision_order = ranks(precision_scores)
        coverage_ranks, _ = ranks(coverage_scores)
        fused = [
            args.precision_weight / (60 + precision_ranks[index])
            + (1 - args.precision_weight) / (60 + coverage_ranks[index])
            for index in range(len(precision_scores))
        ]
        _, fused_order = ranks(fused)
        if args.strategy == "interleave":
            order = [precision_order[0]]
            seen = {precision_order[0]}
            precision_cursor = 1
            coverage_order = sorted(
                range(len(coverage_scores)), key=lambda index: (-coverage_scores[index], index)
            )
            coverage_cursor = 0
            while len(order) < len(precision_order):
                for source, cursor_name in (
                    (precision_order, "precision"),
                    (coverage_order, "coverage"),
                ):
                    cursor = precision_cursor if cursor_name == "precision" else coverage_cursor
                    while cursor < len(source) and source[cursor] in seen:
                        cursor += 1
                    if cursor < len(source):
                        order.append(source[cursor])
                        seen.add(source[cursor])
                        cursor += 1
                    if cursor_name == "precision":
                        precision_cursor = cursor
                    else:
                        coverage_cursor = cursor
                    if len(order) == len(precision_order):
                        break
        else:
            order = [precision_order[0]] + [index for index in fused_order if index != precision_order[0]]
        order_rank = {index: rank for rank, index in enumerate(order)}
        sessions = []
        for index, (precision_session, coverage_session) in enumerate(
            zip(precision_case["sessions"], coverage_case["sessions"])
        ):
            if (
                precision_session.get("content_sha256") != coverage_session.get("content_sha256")
                or len(precision_session.get("turn_scores", []))
                != len(coverage_session.get("turn_scores", []))
            ):
                raise ValueError("precision and coverage cache sessions do not match")
            sessions.append(
                {
                    "content_sha256": precision_session["content_sha256"],
                    "score": 1.0 - 0.8 * order_rank[index] / max(1, len(order) - 1),
                    "turn_scores": precision_session["turn_scores"],
                }
            )
        output_case = {"question_sha256": precision_case["question_sha256"], "sessions": sessions}
        retrieval_query = precision_case.get("retrieval_query") or coverage_case.get("retrieval_query")
        if retrieval_query:
            output_case["retrieval_query"] = retrieval_query
        output_cases.append(output_case)

    output = {
        "format": "domainry-longmemeval-semantic-v1",
        "dataset_sha256": precision["dataset_sha256"],
        "model": precision["model"] + "+rrf+" + coverage["model"],
        "model_revision": precision["model_revision"] + "+" + coverage["model_revision"],
        "query_prefix": f"precision_anchor_coverage_{args.strategy}_{args.precision_weight:.4f}",
        "granularity": "precision_anchor_coverage_" + args.strategy,
        "prefilter_cache_sha256": precision.get("prefilter_cache_sha256", ""),
        "prefilter_sessions": precision.get("prefilter_sessions", 0),
        "prefilter_turns": precision.get("prefilter_turns", 0),
        "source_cache_sha256": [sha256_bytes(precision_bytes), sha256_bytes(coverage_bytes)],
        "query_rewrite_cache_sha256": precision.get("query_rewrite_cache_sha256", ""),
        "query_rewrite_model": precision.get("query_rewrite_model", ""),
        "query_rewrite_revision": precision.get("query_rewrite_revision", ""),
        "query_rewrite_request_mode": precision.get("query_rewrite_request_mode", ""),
        "gold_fields_dropped_during_json_decode": sorted(FORBIDDEN_FIELDS),
        "cases": output_cases,
    }
    Path(args.output).write_text(
        json.dumps(output, ensure_ascii=False, separators=(",", ":")), encoding="utf-8"
    )


if __name__ == "__main__":
    main()
