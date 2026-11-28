#!/usr/bin/env python3
"""Create a pinned, question-only LongMemEval retrieval-query cache."""

import argparse
import hashlib
import json
import re
from pathlib import Path

import torch
from transformers import AutoModelForCausalLM, AutoTokenizer


FORBIDDEN_FIELDS = {"answer", "answer_session_ids", "has_answer", "question_id", "question_type"}
SYSTEM_PROMPT = (
    "Rewrite a question as a short query for searching the user's conversation memory. "
    "Add generic synonyms for the relation and entity type. Preserve temporal, count, comparison, "
    "negation, latest, previous, and current constraints. Never answer the question or guess a name, "
    "number, date, place, product, event, or other fact not written in the question. Output only the "
    "search query, with no explanation."
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


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--model", default="Qwen/Qwen2.5-0.5B-Instruct")
    parser.add_argument("--revision", required=True)
    parser.add_argument("--device", default="cpu")
    parser.add_argument("--batch-size", type=int, default=16)
    parser.add_argument("--max-new-tokens", type=int, default=48)
    return parser.parse_args()


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


def main():
    args = parse_args()
    dataset_bytes = Path(args.input).read_bytes()
    dataset_sha256 = sha256_bytes(dataset_bytes)
    cases = json.loads(dataset_bytes, object_pairs_hook=public_object)
    tokenizer = AutoTokenizer.from_pretrained(args.model, revision=args.revision)
    tokenizer.padding_side = "left"
    if tokenizer.pad_token_id is None:
        tokenizer.pad_token = tokenizer.eos_token
    model = AutoModelForCausalLM.from_pretrained(args.model, revision=args.revision, torch_dtype="auto")
    model.to(args.device)
    model.eval()
    rewrites = []
    for offset in range(0, len(cases), args.batch_size):
        batch = cases[offset : offset + args.batch_size]
        questions = [str(case["question"]).strip() for case in batch]
        prompts = [
            tokenizer.apply_chat_template(
                [{"role": "system", "content": SYSTEM_PROMPT}, {"role": "user", "content": question}],
                tokenize=False,
                add_generation_prompt=True,
            )
            for question in questions
        ]
        encoded = tokenizer(prompts, return_tensors="pt", padding=True, truncation=True, max_length=512).to(args.device)
        with torch.inference_mode():
            generated = model.generate(
                **encoded,
                max_new_tokens=args.max_new_tokens,
                do_sample=False,
                pad_token_id=tokenizer.pad_token_id,
                eos_token_id=tokenizer.eos_token_id,
            )
        suffix = generated[:, encoded["input_ids"].shape[1] :]
        outputs = tokenizer.batch_decode(suffix, skip_special_tokens=True)
        for question, output in zip(questions, outputs, strict=True):
            candidate = " ".join(output.strip().split())
            lowered = candidate.lower()
            safe = bool(candidate) and len(candidate) <= 512 and "answer is" not in lowered and "the answer" not in lowered and safe_expansion(question, candidate)
            query = question
            if safe and candidate.lower() != question.lower():
                combined = question + "\nSearch paraphrase: " + candidate
                if len(combined) <= 1024:
                    query = combined
            rewrites.append({"question_sha256": sha256_bytes(question.encode("utf-8")), "query": query, "expanded": query != question})
    output = {
        "format": "domainry-longmemeval-query-rewrite-v1",
        "dataset_sha256": dataset_sha256,
        "model": args.model,
        "model_revision": args.revision,
        "system_prompt": SYSTEM_PROMPT,
        "gold_fields_dropped_during_json_decode": sorted(FORBIDDEN_FIELDS),
        "cases": rewrites,
    }
    Path(args.output).write_text(json.dumps(output, ensure_ascii=False, separators=(",", ":")), encoding="utf-8")


if __name__ == "__main__":
    main()
