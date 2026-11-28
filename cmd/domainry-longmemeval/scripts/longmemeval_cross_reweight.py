#!/usr/bin/env python3
"""Recover cross-only selected scores from a recorded blended cross cache."""

import argparse
import json
from pathlib import Path

from longmemeval_semantic_cache import FORBIDDEN_FIELDS, sha256_bytes


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", required=True)
    parser.add_argument("--prefilter-cache", required=True)
    parser.add_argument("--output", required=True)
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
    input_bytes = Path(args.input).read_bytes()
    prefilter_bytes = Path(args.prefilter_cache).read_bytes()
    source, prefilter = json.loads(input_bytes), json.loads(prefilter_bytes)
    weight = float(source.get("cross_weight", 0))
    sessions_limit = int(source.get("prefilter_sessions", 0))
    turns_limit = int(source.get("prefilter_turns", 0))
    if (
        source.get("format") != "domainry-longmemeval-semantic-v1"
        or source.get("dataset_sha256") != prefilter.get("dataset_sha256")
        or not 0 < weight <= 1
        or sessions_limit < 1
        or turns_limit < 1
        or len(source.get("cases", [])) != len(prefilter.get("cases", []))
        or source.get("gold_fields_dropped_during_json_decode") != sorted(FORBIDDEN_FIELDS)
    ):
        raise ValueError("cross and prefilter caches are incompatible")
    output_cases = []
    for source_case, prefilter_case in zip(source["cases"], prefilter["cases"], strict=True):
        if source_case["question_sha256"] != prefilter_case["question_sha256"] or len(source_case["sessions"]) != len(prefilter_case["sessions"]):
            raise ValueError("cross and prefilter cases do not match")
        selected_sessions = sorted(range(len(prefilter_case["sessions"])), key=lambda value: prefilter_case["sessions"][value]["score"], reverse=True)[:sessions_limit]
        selected_turns = {
            (session_index, turn_index)
            for session_index in selected_sessions
            for turn_index in sorted(
                range(len(prefilter_case["sessions"][session_index]["turn_scores"])),
                key=lambda value: prefilter_case["sessions"][session_index]["turn_scores"][value],
                reverse=True,
            )[:turns_limit]
        }
        flat_base = [score for session in prefilter_case["sessions"] for score in session["turn_scores"]]
        base = normalize(flat_base)
        offset = 0
        sessions = []
        for session_index, (source_session, prefilter_session) in enumerate(zip(source_case["sessions"], prefilter_case["sessions"], strict=True)):
            if source_session["content_sha256"] != prefilter_session["content_sha256"] or len(source_session["turn_scores"]) != len(prefilter_session["turn_scores"]):
                raise ValueError("cross and prefilter sessions do not match")
            turns = []
            for turn_index, blended in enumerate(source_session["turn_scores"]):
                base_score = base[offset]
                offset += 1
                score = blended
                if (session_index, turn_index) in selected_turns:
                    score = (blended - (1 - weight) * base_score) / weight
                    score = min(1.0, max(0.0, score))
                turns.append(score)
            sessions.append({"content_sha256": source_session["content_sha256"], "score": max(turns, default=0.0), "turn_scores": turns})
        output_cases.append({"question_sha256": source_case["question_sha256"], "sessions": sessions})
    output = dict(source)
    output.update(
        {
            "model": source["model"] + ":cross_only",
            "granularity": source.get("granularity", "cross_encoder") + "_cross_only",
            "cross_weight": 1.0,
            "source_cache_sha256": [sha256_bytes(input_bytes), sha256_bytes(prefilter_bytes)],
            "cases": output_cases,
        }
    )
    Path(args.output).write_text(json.dumps(output, separators=(",", ":")), encoding="utf-8")


if __name__ == "__main__":
    main()
