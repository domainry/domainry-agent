#!/usr/bin/env python3
"""Build a gold-isolated query-rewrite cache through an OpenAI-compatible API."""

import argparse
import concurrent.futures
import hashlib
import json
import os
import re
import time
import urllib.error
import urllib.request
from pathlib import Path


FORBIDDEN_FIELDS = {"answer", "answer_session_ids", "has_answer", "question_id", "question_type"}
SYSTEM_PROMPT = (
    "/no_think\nRewrite each question as a short query for searching the user's conversation memory. "
    "Add generic synonyms for the relation and entity type. Preserve temporal, count, comparison, "
    "negation, latest, previous, and current constraints. Never answer or guess a name, number, date, "
    "place, product, event, or other fact not written in the question. Return strict JSON only: "
    '{"rewrites":[{"index":integer,"query":string}]}. Include every supplied index exactly once.'
)
TEMPORAL_TERMS = {
    "today", "tomorrow", "yesterday", "current", "currently", "previous", "previously", "former",
    "latest", "last", "next", "earlier", "later", "before", "after", "first", "second", "third",
    "monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday",
    "january", "february", "march", "april", "may", "june", "july", "august", "september",
    "october", "november", "december", "year", "month", "week", "day", "hour", "minute",
}
NEGATION_TERMS = {"no", "not", "never", "without", "except"}


def public_object(pairs):
    return {key: value for key, value in pairs if key not in FORBIDDEN_FIELDS}


def sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def safe_expansion(question: str, candidate: str) -> bool:
    question_words = set(re.findall(r"[A-Za-z0-9$]+", question.lower()))
    candidate_words = set(re.findall(r"[A-Za-z0-9$]+", candidate.lower()))
    question_numbers = set(re.findall(r"[$]?[0-9]+(?:[.][0-9]+)?", question))
    candidate_numbers = set(re.findall(r"[$]?[0-9]+(?:[.][0-9]+)?", candidate))
    if not candidate_numbers.issubset(question_numbers):
        return False
    question_proper = {value.lower() for value in re.findall(r"(?<!^)\b[A-Z][A-Za-z0-9$-]*", question)}
    candidate_proper = {value.lower() for value in re.findall(r"(?<!^)\b[A-Z][A-Za-z0-9$-]*", candidate)}
    if not candidate_proper.issubset(question_proper | question_words):
        return False
    if not (candidate_words & TEMPORAL_TERMS).issubset(question_words & TEMPORAL_TERMS):
        return False
    if (candidate_words & NEGATION_TERMS) != (question_words & NEGATION_TERMS):
        return False
    return True


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--base-url", default=os.environ.get("LONGMEMEVAL_LLM_BASE_URL", ""))
    parser.add_argument("--model", default=os.environ.get("LONGMEMEVAL_LLM_MODEL", ""))
    parser.add_argument("--batch-size", type=int, default=20)
    parser.add_argument("--concurrency", type=int, default=4)
    parser.add_argument("--timeout-seconds", type=int, default=180)
    return parser.parse_args()


def decode_json_content(content: str):
    content = content.strip()
    if content.startswith("```"):
        content = re.sub(r"^```(?:json)?\s*", "", content)
        content = re.sub(r"\s*```$", "", content)
    start, end = content.find("{"), content.rfind("}")
    if start < 0 or end < start:
        raise ValueError("rewrite response did not contain JSON")
    return json.loads(content[start : end + 1])


def request_batch(base_url, api_key, model, timeout, batch):
    supplied = [{"index": index, "question": question} for index, question in batch]
    payload = {
        "model": model,
        "temperature": 0,
        "max_tokens": max(512, len(batch) * 80),
        "messages": [
            {"role": "system", "content": SYSTEM_PROMPT},
            {"role": "user", "content": json.dumps({"questions": supplied}, ensure_ascii=False, separators=(",", ":"))},
        ],
    }
    raw = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    url = base_url.rstrip("/") + "/chat/completions"
    request = urllib.request.Request(url, data=raw, method="POST", headers={"Authorization": "Bearer " + api_key, "Content-Type": "application/json"})
    last_error = None
    for attempt in range(3):
        try:
            with urllib.request.urlopen(request, timeout=timeout) as response:
                decoded = json.loads(response.read())
            content = decoded["choices"][0]["message"]["content"]
            result = decode_json_content(content)
            values = result.get("rewrites", [])
            by_index = {int(value["index"]): str(value["query"]).strip() for value in values}
            expected = {index for index, _ in batch}
            if set(by_index) != expected:
                raise ValueError("rewrite response indexes do not match request")
            return by_index
        except (OSError, KeyError, TypeError, ValueError, json.JSONDecodeError, urllib.error.HTTPError) as error:
            last_error = error
            if attempt < 2:
                time.sleep(2**attempt)
    raise RuntimeError("query rewrite request failed") from last_error


def main():
    args = parse_args()
    api_key = os.environ.get("LONGMEMEVAL_LLM_API_KEY", "").strip()
    if not args.base_url or not args.model or not api_key or args.batch_size < 1 or args.concurrency < 1:
        raise ValueError("online query rewrite configuration is incomplete")
    dataset_bytes = Path(args.input).read_bytes()
    dataset_sha256 = sha256_bytes(dataset_bytes)
    cases = json.loads(dataset_bytes, object_pairs_hook=public_object)
    indexed = [(index, str(case["question"]).strip()) for index, case in enumerate(cases)]
    batches = [indexed[offset : offset + args.batch_size] for offset in range(0, len(indexed), args.batch_size)]
    candidates = {}
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.concurrency) as executor:
        futures = [executor.submit(request_batch, args.base_url, api_key, args.model, args.timeout_seconds, batch) for batch in batches]
        for future in concurrent.futures.as_completed(futures):
            candidates.update(future.result())
    rewrites = []
    for index, question in indexed:
        candidate = " ".join(candidates[index].split())
        lowered = candidate.lower()
        accepted = bool(candidate) and len(candidate) <= 512 and "answer is" not in lowered and "the answer" not in lowered and safe_expansion(question, candidate)
        query = question
        if accepted and candidate.lower() != question.lower():
            combined = question + "\nSearch paraphrase: " + candidate
            if len(combined) <= 1024:
                query = combined
        rewrites.append(
            {
                "question_sha256": sha256_bytes(question.encode("utf-8")),
                "query": query,
                "candidate": candidate,
                "expanded": query != question,
                "accepted": accepted,
            }
        )
    output = {
        "format": "domainry-longmemeval-query-rewrite-v1",
        "dataset_sha256": dataset_sha256,
        "model": args.model,
        "model_revision": "service_configured",
        "system_prompt": SYSTEM_PROMPT,
        "request_mode": "question_only_openai_compatible",
        "gold_fields_dropped_during_json_decode": sorted(FORBIDDEN_FIELDS),
        "cases": rewrites,
    }
    Path(args.output).write_text(json.dumps(output, ensure_ascii=False, separators=(",", ":")), encoding="utf-8")


if __name__ == "__main__":
    main()
