# Knowledge reads in background Tasks

A Task Agent may explicitly declare `AgentToolKnowledgeSearch` and
`AgentToolKnowledgeRead` from the Agent SDK catalog. The local ModelTaskRunner
advertises these as read-only tools and forwards execution through the same
TaskToolService, short-lived Task credential, live host authorization, lease,
tool-call ledger, timeout and shared tool budget as record reads.

Inputs are exactly:

```json
{"library_id":"authorized-library-id","query":"question"}
```

```json
{"library_id":"authorized-library-id","document_id":"cited-document-id"}
```

The host must provide library-aware Knowledge search/read and revalidate the
retrieval result under the current identity before returning normalized
citations. Provider payloads, credentials, caller-selected permission IDs,
URLs and alternative identities are not accepted as arguments. Project Object
grants are not Knowledge grants; declaring these tools never grants library
membership. An unconfigured host fails rather than returning an invented
successful empty result.

These tools do not require an allowed project Object. Record tools continue to
require their exact allowed Object scope. No Knowledge write tool is registered
and no allowed Action is implied by these reads.

Tool results remain untrusted content. A Task consumer must not treat model
citations as authoritative just because retrieval tools are available: preserve
actual tool receipts and source versions, and check current access when later
displaying or releasing protected content. Tool availability alone does not
certify a downstream answer, customer audience or resolution.
