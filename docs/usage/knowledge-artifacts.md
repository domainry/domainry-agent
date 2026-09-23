# When should Agent work use knowledge libraries, private attachments, or saved artifacts?

## Problems solved

- Separates shared reference knowledge, conversation-private source files, and versioned user outputs so their authorization, lifecycle, and reuse are not conflated.

## Business scenarios

- A policy assistant searches a team knowledge library whose membership is checked against current Identity authorization.
- A user attaches a private PDF to one conversation, then saves the resulting analysis as a versioned artifact for later export.
- A policy answer preserves the exact source-document revision, while an updated policy does not rewrite earlier answer evidence.

## Use when

Use these capabilities when Agent reasoning needs governed reference material, private per-conversation inputs, or a durable editable output.

## Do not use when

Do not treat an attachment as automatically indexed knowledge, an artifact as the source business record, or a library role as a substitute for source-record authorization.

## How to use

Choose exactly one ownership model: library documents for governed reusable knowledge, attachments for private original files scoped to one conversation, and artifacts for user-owned versioned outputs. Recheck source authorization on every read/export.

## Adaptation cookbook

| Content requirement | Adapt with | Concrete implementation | Wrong adaptation |
| --- | --- | --- | --- |
| Team reuses approved policy documents | Knowledge library | Grant library membership roles, preserve document/source identity, and query only currently authorized content | Uploading documents into one user's conversation and treating them as shared |
| User analyzes a private contract PDF | Conversation attachment | Store the original under the trusted runtime/workspace/user/conversation tuple; allow inspect/download/delete only to the owner | Exposing a host path, guessing an upstream ACL ID, or automatically making it shared knowledge |
| User keeps and edits a generated brief | Versioned artifact | Save typed content with immutable versions and current source references; export an authorized version | Writing the brief back into the contract business Object or overwriting history in place |
| Product stores authoritative signed contracts | Business Object plus file Field | Keep the contract and file reference under the owning business domain and its retention policy | Using an Agent artifact as the system of record |

## Example

The policy assistant retrieves `travel-policy@2026-04` from a team library and cites that immutable source revision. A user's private contract PDF remains attached only to the authorized conversation and is not indexed into shared knowledge. The generated comparison is saved as `contract-analysis@1` with source/version evidence and may later be exported, but it is derived work—not the signed contract business record and not Audit evidence. Updating the policy creates a new knowledge revision; deleting the conversation follows attachment policy without silently deleting the shared library or authoritative contract.

## Permissions and scope

Library membership, attachment ownership, artifact ownership, and underlying source-record access are independently enforced. Export never widens any of those scopes.

## Boundaries

Knowledge libraries hold reusable reference content; attachments hold private originals; artifacts hold derived user work. None replaces Runtime business records or Audit evidence.
