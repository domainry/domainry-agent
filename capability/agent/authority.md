# What data and tools may an Agent use?

## Problems solved

- Prevents an Agent from turning model access into unrestricted data access, credential access, or arbitrary tool execution.

## Business scenarios

- An account-manager copilot may query only customers visible through the manager's current Role and data scope.
- A service Agent receives a bounded tool allowlist without inheriting administrator authority or raw provider credentials.

## Use when

Use guarded authority whenever an Agent reads business data, calls a tool, or requests an effect.

## Do not use when

Do not give a model a database connection, provider credential, generic Runtime client, or unrestricted Connector.

## How to use

Expose named, typed host capabilities. Resolve the Identity principal and Workspace on every call, apply normal operation permission/data scope, set budgets and idempotency, and record the result through Audit.

## Adaptation cookbook

| Agent requirement | Adapt with | Concrete implementation | Wrong adaptation |
| --- | --- | --- | --- |
| A manager asks for a summary of visible customers. | Initiating-principal scope | The customer query tool executes with the manager's exact `customer.read` grant and row scope; the Agent sees only returned rows. | Accepting an organization ID from the prompt or filtering unrestricted results after retrieval. |
| A service Agent triages cases with two approved tools. | Service Role intersected with Agent/Skill/task allowlists | Give the service Role only case read and proposal permissions, then list only those tools at every Agent layer. | Assuming a tool allowlist grants the underlying Runtime permission. |
| A provider call needs a credential. | Integration connection and typed tool | Let Integration resolve the credential and perform the operation; expose only typed input/output to the Agent. | Putting API keys, model tokens, or secret references in prompts or Agent configuration. |

## Example

An account-manager copilot can call “list visible customers” under the manager’s exact `customer.read` permission. It cannot pass an arbitrary organization ID or query raw customer tables.

## Permissions and scope

Missing permission means deny. `all` remains all matching rows in one Workspace, while `owner`, `org`, `org_child`, and `target_org` retain their normal Runtime meanings.

## Boundaries

Agent owns reasoning and orchestration. Identity owns principals, source modules own visible records, and the invoked Business Operation owns effects.
