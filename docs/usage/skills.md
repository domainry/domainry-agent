# When should an Agent capability become a versioned Skill?

## Problems solved

- Makes reusable instructions, tool allowlists, prompts, and delegation configuration discoverable, evaluable, versioned, and rollbackable instead of embedding mutable behavior in an Agent record or system prompt.

## Business scenarios

- A support team publishes an evaluated case-summary Skill used by several Agents with the same bounded read tools.
- An operator rolls back a newly published delegation prompt after evaluation shows lower accuracy or unsafe tool selection.
- A customer-service Skill may draft a reply but cannot send it unless every authority layer and side-effect mode also permits the exact send Action.

## Use when

Use a Skill when a reusable Agent behavior needs explicit versioning, evaluation evidence, on-demand loading, and controlled publication.

## Do not use when

Do not create a Skill for one deterministic Operation, one user's temporary instruction, or a capability whose underlying Role/tool authority does not exist.

## How to use

Publish summary metadata for selection, load exact versioned instructions only after selection, validate the declared tool/object/action allowlists, attach evaluation evidence, and make rollback select a prior immutable version.

## Adaptation cookbook

| Capability requirement | Adapt with | Concrete implementation | Wrong adaptation |
| --- | --- | --- | --- |
| Several Agents summarize cases consistently | Versioned Skill | Declare bounded instructions, input/output expectations, allowed Objects/Actions, evaluation set, and immutable version | Copy-pasting prompts into each Agent definition |
| A new prompt performs worse in evaluation | Publication rollback | Retain both versions and evidence; repoint publication to the last accepted version without mutating history | Editing the published prompt in place or hiding failed evaluation |
| Skill wants to issue refunds | Skill allowlist plus authorized Operation/proposal | Allow only the named proposal or Operation tool and retain normal Role/approval checks | Assuming Skill publication grants business permission |
| One fixed calculation has no model reasoning | Business Operation | Implement deterministic typed code and tests | Wrapping the calculation in a Skill to gain flexibility |

## Example

`support.summary@3` declares versioned instructions, typed input/output, and read-only case tools. Agent `support_copilot` selects it; Task `case-88-summary` binds the case input; its Entrypoint determines which users may start it; a background run, if allowed, uses a least-privilege Service Principal. Version 4 is evaluated before publication; rollback repoints new selection to version 3 while running and historical Tasks retain their original version evidence. Draft generation does not imply send authority: Skill allowlist, Agent/task allowlists, Role/Action permission, connection, approval, and side-effect mode must all allow the exact effect. One temporary personal prompt is not a published Skill, and deterministic core rules remain code.

## Permissions and scope

Skill discovery may expose summary metadata only. Instruction loading, draft, evaluation, publish, and rollback are distinct operations, and effective tool authority remains the intersection with Identity and task/Agent allowlists.

## Boundaries

Skills define reusable model behavior; they do not own business data, grant permissions, or replace deterministic domain code.
