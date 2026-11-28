#!/usr/bin/env python3
"""Merge independently generated development and holdout semantic caches."""

import argparse
import hashlib
import json
from pathlib import Path


FORBIDDEN_FIELDS = {"answer", "answer_session_ids", "has_answer", "question_id", "question_type"}


def public_object(pairs):
    return {key: value for key, value in pairs if key not in FORBIDDEN_FIELDS}


def sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", required=True)
    parser.add_argument("--development-cache", required=True)
    parser.add_argument("--holdout-cache", required=True)
    parser.add_argument("--output", required=True)
    return parser.parse_args()


def main():
    args = parse_args()
    dataset_bytes = Path(args.input).read_bytes()
    dataset_sha256 = sha256_bytes(dataset_bytes)
    cases = json.loads(dataset_bytes, object_pairs_hook=public_object)
    development_bytes = Path(args.development_cache).read_bytes()
    holdout_bytes = Path(args.holdout_cache).read_bytes()
    development = json.loads(development_bytes)
    holdout = json.loads(holdout_bytes)
    if (
        development.get("format") != "domainry-longmemeval-semantic-v1"
        or holdout.get("format") != "domainry-longmemeval-semantic-v1"
        or development.get("dataset_sha256") != dataset_sha256
        or holdout.get("dataset_sha256") != dataset_sha256
        or len(development.get("cases", [])) != len(cases)
        or len(holdout.get("cases", [])) != len(cases)
    ):
        raise ValueError("split caches do not match the dataset")

    merged_cases = []
    for index, case in enumerate(cases):
        question = str(case["question"])
        question_sha256 = sha256_bytes(question.encode("utf-8"))
        development_case = development["cases"][index]
        holdout_case = holdout["cases"][index]
        if (
            development_case.get("question_sha256") != question_sha256
            or holdout_case.get("question_sha256") != question_sha256
            or len(development_case.get("sessions", [])) != len(holdout_case.get("sessions", []))
        ):
            raise ValueError("split cache case does not match the dataset")
        for development_session, holdout_session in zip(
            development_case["sessions"], holdout_case["sessions"]
        ):
            if development_session.get("content_sha256") != holdout_session.get("content_sha256"):
                raise ValueError("split cache session content hashes do not match")
        is_development = hashlib.sha256(question.encode("utf-8")).digest()[0] % 10 < 7
        merged_cases.append(development_case if is_development else holdout_case)

    output = dict(development)
    output["model"] = development["model"]
    output["model_revision"] = development["model_revision"]
    output["granularity"] = "candidate_session_online_llm_rerank_split_merged"
    output["source_cache_sha256"] = [sha256_bytes(development_bytes), sha256_bytes(holdout_bytes)]
    output["rerank_split"] = "all"
    output["repaired_rankings"] = int(development.get("repaired_rankings", 0)) + int(
        holdout.get("repaired_rankings", 0)
    )
    output["fallback_rankings"] = int(development.get("fallback_rankings", 0)) + int(
        holdout.get("fallback_rankings", 0)
    )
    output["cases"] = merged_cases
    Path(args.output).write_text(
        json.dumps(output, ensure_ascii=False, separators=(",", ":")), encoding="utf-8"
    )


if __name__ == "__main__":
    main()
