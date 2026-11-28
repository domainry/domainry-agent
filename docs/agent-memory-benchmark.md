# Agent MemoryBench

MemoryBench measures whether memory improves the final answer, not merely
whether persistence and recovery code runs. Use synthetic facts only; never
copy customer or credential data into a benchmark fixture.

## Required comparison arms

Each scenario is replayed with the same conversation/task history in four
independent environments:

1. `no_memory`: memory reference and creation disabled;
2. `domainry`: the implementation under test;
3. `oracle`: a reviewer supplies the exact relevant context;
4. `chatgpt`: optional black-box comparison using the same synthetic facts.

Score final-answer correctness from 0 to 1 using a fixed rubric. The evaluator
normalizes Domainry and ChatGPT against the no-memory and oracle bounds:

```text
effectiveness = (actual - no_memory) / (oracle - no_memory)
```

## Scenario coverage

The suite must include explicit saves, implicit stable preferences, transient
details that must not be saved, corrections, contradictions, expiry,
do-not-mention, source deletion, history detail lookup, project-only isolation,
cross-user isolation, and product-owned business memory.

For each run, export the selected memory IDs and source checks from the frozen
run evidence. Candidate facts are canonical reviewer labels rather than model-
generated IDs, so equivalent wording can be compared across systems.

## Input

```json
{
  "cases": [
    {
      "id": "preference-correction",
      "expected_memory_ids": ["preference-current"],
      "selected_memory_ids": ["preference-current"],
      "forbidden_memory_ids": ["preference-superseded", "other-user-memory"],
      "expected_candidate_facts": ["prefers concise Chinese"],
      "proposed_candidate_facts": ["prefers concise Chinese"],
      "source_checks": [
        {"source_id": "conversation-a", "correct": true}
      ],
      "answer_scores": {
        "no_memory": 0.4,
        "domainry": 0.85,
        "oracle": 0.9,
        "chatgpt": 0.88
      }
    }
  ]
}
```

Run:

```bash
go run ./cmd/domainry-memory-eval -input /absolute/path/to/memory-benchmark.json
```

The command exits non-zero unless all launch gates pass: zero forbidden
selections, retrieval recall at least 0.85, retrieval precision at least 0.75,
candidate recall at least 0.80, candidate precision at least 0.95, source
accuracy at least 0.98, and normalized Domainry effectiveness at least 0.75.
