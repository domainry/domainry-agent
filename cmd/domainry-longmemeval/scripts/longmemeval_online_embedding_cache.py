#!/usr/bin/env python3
"""Build a gold-isolated LongMemEval cache with an OpenAI-compatible embedding API."""

import argparse
import concurrent.futures
import hashlib
import json
import math
import os
import re
import time
import urllib.error
import urllib.request
from pathlib import Path


FORBIDDEN_FIELDS = {"answer", "answer_session_ids", "has_answer", "question_id", "question_type"}
DEFAULT_QUERY_PREFIX = (
    "Instruct: Retrieve conversation sessions containing evidence needed to answer the memory query.\nQuery: "
)


def public_object(pairs):
    return {key: value for key, value in pairs if key not in FORBIDDEN_FIELDS}


def sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


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
    parser.add_argument("--query-rewrite-cache", required=True)
    parser.add_argument("--base-url", default=os.environ.get("LONGMEMEVAL_EMBEDDING_BASE_URL", ""))
    parser.add_argument("--model", default=os.environ.get("LONGMEMEVAL_EMBEDDING_MODEL", ""))
    parser.add_argument("--dimensions", type=int, default=1024)
    parser.add_argument("--batch-size", type=int, default=10)
    parser.add_argument("--candidate-sessions", type=int, default=20)
    parser.add_argument("--max-session-chars", type=int, default=8000)
    parser.add_argument("--projection-turns", type=int, default=3)
    parser.add_argument("--max-turn-chars", type=int, default=1600)
    parser.add_argument("--query-prefix", default=DEFAULT_QUERY_PREFIX)
    parser.add_argument("--turn-scoring", choices=("lexical", "embedding"), default="lexical")
    parser.add_argument("--concurrency", type=int, default=12)
    parser.add_argument("--timeout-seconds", type=int, default=90)
    parser.add_argument("--checkpoint")
    return parser.parse_args()


def request_embeddings(base_url, api_key, model, dimensions, timeout, texts):
    payload = {
        "model": model,
        "input": texts,
        "dimensions": dimensions,
        "encoding_format": "float",
    }
    raw = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    request = urllib.request.Request(
        base_url.rstrip("/") + "/embeddings",
        data=raw,
        method="POST",
        headers={"Authorization": "Bearer " + api_key, "Content-Type": "application/json"},
    )
    last_error = None
    for attempt in range(8):
        try:
            with urllib.request.urlopen(request, timeout=timeout) as response:
                decoded = json.loads(response.read())
            rows = sorted(decoded["data"], key=lambda value: int(value["index"]))
            vectors = [row["embedding"] for row in rows]
            if len(vectors) != len(texts) or any(len(vector) != dimensions for vector in vectors):
                raise ValueError("embedding response shape does not match request")
            return vectors
        except (OSError, KeyError, TypeError, ValueError, json.JSONDecodeError, urllib.error.HTTPError) as error:
            last_error = error
            if attempt < 7:
                retry_after = 0
                if isinstance(error, urllib.error.HTTPError) and error.code == 429:
                    try:
                        retry_after = int(error.headers.get("Retry-After", "0"))
                    except ValueError:
                        retry_after = 0
                time.sleep(max(retry_after, min(30, 2**attempt)))
    raise RuntimeError("online embedding request failed") from last_error


def embed_texts(base_url, api_key, model, dimensions, timeout, batch_size, texts):
    vectors = []
    for offset in range(0, len(texts), batch_size):
        vectors.extend(
            request_embeddings(
                base_url,
                api_key,
                model,
                dimensions,
                timeout,
                texts[offset : offset + batch_size],
            )
        )
    return vectors


def cosine(left, right):
    numerator = sum(a * b for a, b in zip(left, right))
    left_norm = math.sqrt(sum(value * value for value in left))
    right_norm = math.sqrt(sum(value * value for value in right))
    if left_norm == 0 or right_norm == 0:
        return 0.0
    return numerator / (left_norm * right_norm)


def session_text(query, date, turns, limit, projection_turns):
    selected = sorted(
        range(len(turns)),
        key=lambda index: lexical_score(query, str(turns[index].get("content", ""))),
        reverse=True,
    )[:projection_turns]
    text = "Conversation date: " + str(date) + "\n" + "\n".join(
        f'{turns[index].get("role", "")}: {turns[index].get("content", "")}'
        for index in sorted(selected)
    )
    return "Conversation session:\n" + text[:limit]


def lexical_score(query, content):
    query_terms = set(re.findall(r"[a-z0-9]+", query.lower()))
    content_terms = set(re.findall(r"[a-z0-9]+", content.lower()))
    if not query_terms:
        return 0.0
    return len(query_terms & content_terms) / math.sqrt(len(query_terms) * max(1, len(content_terms)))


def build_case(args, api_key, case, rewrite_case):
    question = str(case["question"])
    if rewrite_case.get("question_sha256") != sha256_bytes(question.encode("utf-8")):
        raise ValueError("query rewrite cache case does not match the dataset")
    retrieval_query = str(rewrite_case.get("query", ""))
    if not retrieval_query:
        raise ValueError("query rewrite is empty")
    sessions = case["haystack_sessions"]
    query_text = args.query_prefix + retrieval_query
    session_texts = [
        session_text(
            retrieval_query,
            case["haystack_dates"][index],
            turns,
            args.max_session_chars,
            args.projection_turns,
        )
        for index, turns in enumerate(sessions)
    ]
    vectors = embed_texts(
        args.base_url,
        api_key,
        args.model,
        args.dimensions,
        args.timeout_seconds,
        args.batch_size,
        [query_text] + session_texts,
    )
    query_vector = vectors[0]
    session_scores = [cosine(query_vector, vector) for vector in vectors[1:]]
    turn_scores = [
        [lexical_score(retrieval_query, str(turn.get("content", ""))) for turn in turns]
        for turns in sessions
    ]
    if args.turn_scoring == "embedding":
        selected = sorted(range(len(sessions)), key=lambda value: session_scores[value], reverse=True)[
            : args.candidate_sessions
        ]
        turn_layout = []
        turn_texts = []
        for session_index in selected:
            for turn_index, turn in enumerate(sessions[session_index]):
                turn_layout.append((session_index, turn_index))
                content = str(turn.get("content", ""))[: args.max_turn_chars]
                turn_texts.append(
                    "Conversation turn on "
                    + str(case["haystack_dates"][session_index])
                    + ":\n"
                    + f'{turn.get("role", "")}: {content}'
                )
        turn_vectors = embed_texts(
            args.base_url,
            api_key,
            args.model,
            args.dimensions,
            args.timeout_seconds,
            args.batch_size,
            turn_texts,
        )
        for (session_index, turn_index), vector in zip(turn_layout, turn_vectors):
            turn_scores[session_index][turn_index] = cosine(query_vector, vector)
        for session_index in selected:
            session_scores[session_index] = max(
                session_scores[session_index], max(turn_scores[session_index], default=0.0)
            )
    output_sessions = []
    for session_index, turns in enumerate(sessions):
        output_sessions.append(
            {
                "content_sha256": session_hash(
                    case["haystack_session_ids"][session_index],
                    case["haystack_dates"][session_index],
                    turns,
                ),
                "score": session_scores[session_index],
                "turn_scores": turn_scores[session_index],
            }
        )
    return {
        "question_sha256": sha256_bytes(question.encode("utf-8")),
        "retrieval_query": retrieval_query,
        "sessions": output_sessions,
    }


def main():
    args = parse_args()
    api_key = os.environ.get("LONGMEMEVAL_EMBEDDING_API_KEY", "").strip()
    if (
        not args.base_url
        or not args.model
        or not api_key
        or args.dimensions < 1
        or args.batch_size < 1
        or args.candidate_sessions < 1
        or args.projection_turns < 1
        or args.concurrency < 1
    ):
        raise ValueError("online embedding configuration is incomplete")
    dataset_bytes = Path(args.input).read_bytes()
    dataset_sha256 = sha256_bytes(dataset_bytes)
    cases = json.loads(dataset_bytes, object_pairs_hook=public_object)
    rewrite_bytes = Path(args.query_rewrite_cache).read_bytes()
    rewrite_sha256 = sha256_bytes(rewrite_bytes)
    rewrites = json.loads(rewrite_bytes)
    if rewrites.get("dataset_sha256") != dataset_sha256 or len(rewrites.get("cases", [])) != len(cases):
        raise ValueError("query rewrite cache does not match the dataset")

    checkpoint_path = Path(args.checkpoint or (args.output + ".checkpoint"))
    checkpoint_metadata = {
        "dataset_sha256": dataset_sha256,
        "query_rewrite_cache_sha256": rewrite_sha256,
        "model": args.model,
        "dimensions": args.dimensions,
        "candidate_sessions": args.candidate_sessions,
        "max_session_chars": args.max_session_chars,
        "projection_turns": args.projection_turns,
        "max_turn_chars": args.max_turn_chars,
        "query_prefix": args.query_prefix,
        "turn_scoring": args.turn_scoring,
    }
    output_cases = [None] * len(cases)
    if checkpoint_path.exists():
        checkpoint = json.loads(checkpoint_path.read_bytes())
        if checkpoint.get("metadata") != checkpoint_metadata or len(checkpoint.get("cases", [])) != len(cases):
            raise ValueError("online embedding checkpoint does not match this run")
        output_cases = checkpoint["cases"]
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.concurrency) as executor:
        future_indexes = {
            executor.submit(build_case, args, api_key, case, rewrites["cases"][index]): index
            for index, case in enumerate(cases)
            if output_cases[index] is None
        }
        for future in concurrent.futures.as_completed(future_indexes):
            output_cases[future_indexes[future]] = future.result()
            checkpoint_path.write_text(
                json.dumps({"metadata": checkpoint_metadata, "cases": output_cases}, separators=(",", ":")),
                encoding="utf-8",
            )

    output = {
        "format": "domainry-longmemeval-semantic-v1",
        "dataset_sha256": dataset_sha256,
        "model": args.model,
        "model_revision": "service_configured",
        "query_prefix": args.query_prefix,
        "granularity": "online_session_with_" + args.turn_scoring + "_turn_projection",
        "query_rewrite_cache_sha256": rewrite_sha256,
        "query_rewrite_model": str(rewrites.get("model", "")),
        "query_rewrite_revision": str(rewrites.get("model_revision", "")),
        "query_rewrite_request_mode": "question_only_openai_compatible+online_embeddings",
        "embedding_dimensions": args.dimensions,
        "candidate_sessions": args.candidate_sessions,
        "projection_turns": args.projection_turns,
        "turn_scoring": args.turn_scoring,
        "gold_fields_dropped_during_json_decode": sorted(FORBIDDEN_FIELDS),
        "cases": output_cases,
    }
    Path(args.output).write_text(
        json.dumps(output, ensure_ascii=False, separators=(",", ":")), encoding="utf-8"
    )


if __name__ == "__main__":
    main()
