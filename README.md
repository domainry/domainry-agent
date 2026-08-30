# domainry-agent

Source-owned Agent Runner / AI Gateway implementation for Domainry.

- `module`: in-process Binding selected by project composition.
- `remote`: SaaS Binding selected by project composition.
- `server`: authenticated SaaS protocol endpoint with Descriptor handshake.
- `cmd/domainry-agent`: standalone SaaS process entrypoint.
- `internal/provider`: provider HTTP protocol, result normalization and stable error classification.

Runtime business state remains in `domainry-runtime`; this module does not own workflow, authorization, approvals, task leases or Runtime tool execution.
