# Task Knowledge execution receipts

Task Knowledge receipts come from the trusted host's typed `Knowledge` field, not the model's tool output or final Task JSON. Agent validates the library, operation, requested document, terminal tool status and bounded reference schema before saving it with the fenced tool invocation. Ordinary record tools cannot attach a Knowledge receipt. A failed host call retains its failure and no successful citation receipt.

The ledger stores only source identifiers, document/page versions, chunk/record references, content/source revision hashes and the source scope hash. Search with no hits has an explicit empty citation collection. Provider text, title, URL, credentials and permission bundles are not added to this receipt. The existing tool response remains unchanged; the receipt is not an extra model-visible field or a source access grant.

Fenced persistence clones the receipt into the committed Task evidence. Completion callbacks reload that committed ledger, so the same receipt reaches the Workflow owner even when the worker's in-memory snapshot predates tool calls. Replaying a finished tool call cannot replace its saved references. Current Knowledge authorization must still be checked before later reading or releasing a source.

Requires domainry-agent-sdk v0.1.59 and a host that supplies typed Task Knowledge receipts. A host returning only model-visible citation text is rejected for Knowledge calls instead of manufacturing a trusted receipt.
