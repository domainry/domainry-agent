#!/usr/bin/env python3
"""Rerank non-gold LongMemEval candidate sentences with a local cross-encoder."""

import argparse
import json
import re
from pathlib import Path

from sentence_transformers import CrossEncoder

from longmemeval_semantic_cache import (
    FORBIDDEN_FIELDS,
    public_object,
    session_hash,
    sha256_bytes,
)


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--prefilter-cache", required=True)
    parser.add_argument("--model", default="cross-encoder/ms-marco-MiniLM-L6-v2")
    parser.add_argument("--revision", required=True)
    parser.add_argument("--device", default="cpu")
    parser.add_argument("--batch-size", type=int, default=128)
    parser.add_argument("--prefilter-sessions", type=int, default=20)
    parser.add_argument("--prefilter-turns", type=int, default=3)
    parser.add_argument("--cross-weight", type=float, default=0.65)
    parser.add_argument("--granularity", choices=("sentence", "turn"), default="sentence")
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
    if not 0 <= args.cross_weight <= 1:
        raise ValueError("cross weight must be between zero and one")
    dataset_bytes = Path(args.input).read_bytes()
    dataset_sha256 = sha256_bytes(dataset_bytes)
    cases = json.loads(dataset_bytes, object_pairs_hook=public_object)
    prefilter_bytes = Path(args.prefilter_cache).read_bytes()
    prefilter_sha256 = sha256_bytes(prefilter_bytes)
    prefilter = json.loads(prefilter_bytes)
    if prefilter.get("dataset_sha256") != dataset_sha256 or len(prefilter.get("cases", [])) != len(cases):
        raise ValueError("prefilter cache does not match the dataset")

    pairs = []
    layouts = []
    for case_index, case in enumerate(cases):
        question = str(case["question"])
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
        case_layout = {}
        for session_index, turns in enumerate(case["haystack_sessions"]):
            for turn_index, turn in enumerate(turns):
                if (session_index, turn_index) not in selected_turns:
                    continue
                content = str(turn.get("content", ""))
                fragments = [content]
                if args.granularity == "sentence":
                    fragments = [value.strip() for value in re.split(r"(?<=[.!?])\s+|\n+", content) if value.strip()]
                    if not fragments:
                        fragments = [content]
                start = len(pairs)
                pairs.extend((question, f'{turn.get("role", "")}: {fragment}') for fragment in fragments)
                case_layout[(session_index, turn_index)] = (start, len(pairs))
        layouts.append(case_layout)

    model = CrossEncoder(args.model, revision=args.revision, device=args.device)
    cross_scores = model.predict(pairs, batch_size=args.batch_size, show_progress_bar=True).astype(float).tolist()
    output_cases = []
    for case_index, case in enumerate(cases):
        question = str(case["question"])
        cached_sessions = prefilter["cases"][case_index]["sessions"]
        base_flat = [score for session in cached_sessions for score in session["turn_scores"]]
        base_normalized = normalize(base_flat)
        base_offset = 0
        cross_keys = list(layouts[case_index])
        cross_maximums = [max(cross_scores[start:end], default=0.0) for start, end in layouts[case_index].values()]
        cross_normalized = dict(zip(cross_keys, normalize(cross_maximums), strict=True))
        output_sessions = []
        for session_index, turns in enumerate(case["haystack_sessions"]):
            turn_scores = []
            for turn_index in range(len(turns)):
                base_score = base_normalized[base_offset]
                base_offset += 1
                if (session_index, turn_index) in cross_normalized:
                    score = (1 - args.cross_weight) * base_score + args.cross_weight * cross_normalized[(session_index, turn_index)]
                else:
                    score = base_score
                turn_scores.append(score)
            output_sessions.append(
                {
                    "content_sha256": session_hash(case["haystack_session_ids"][session_index], case["haystack_dates"][session_index], turns),
                    "score": max(turn_scores, default=0.0),
                    "turn_scores": turn_scores,
                }
            )
        output_cases.append({"question_sha256": sha256_bytes(question.encode("utf-8")), "sessions": output_sessions})

    output = {
        "format": "domainry-longmemeval-semantic-v1",
        "dataset_sha256": dataset_sha256,
        "model": args.model + "+" + prefilter["model"],
        "model_revision": args.revision + "+" + prefilter["model_revision"],
        "granularity": "candidate_" + args.granularity + "_cross_encoder",
        "device": args.device,
        "prefilter_cache_sha256": prefilter_sha256,
        "prefilter_sessions": args.prefilter_sessions,
        "prefilter_turns": args.prefilter_turns,
        "cross_weight": args.cross_weight,
        "gold_fields_dropped_during_json_decode": sorted(FORBIDDEN_FIELDS),
        "cases": output_cases,
    }
    Path(args.output).write_text(json.dumps(output, separators=(",", ":")), encoding="utf-8")


if __name__ == "__main__":
    main()
