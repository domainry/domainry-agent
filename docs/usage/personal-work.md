# When should personal work use Agent background tasks or todos?

## Problems solved

- Separates durable Agent execution from user-owned work planning so a todo is not mistaken for a running job and a background run is not used as a general task list.

## Business scenarios

- A user sends a portfolio analysis to the background and later inspects, cancels, or resumes its durable execution.
- A user keeps an ordered personal follow-up list with due dates that does not execute tools by itself.
- A business Workflow task and a scheduled recurring job remain their own shared/process and time-triggered owners rather than being copied into personal todos.

## Use when

Use background tasks for Agent execution that outlives the request; use personal todos for owner-scoped planning and completion state.

## Do not use when

Do not use todos as Scheduler jobs, background tasks as Workflow business state, or either capability as a shared project work queue.

## How to use

For background work, preserve source conversation/run boundaries, checkpoints, cancellation, and terminal outcome. For todos, preserve owner, order, deadline, batch mutation, and completion without granting execution.

## Adaptation cookbook

| Personal-work requirement | Adapt with | Concrete implementation | Wrong adaptation |
| --- | --- | --- | --- |
| Analysis continues after the user closes the page | Background Agent task | Create a durable execution linked to its source conversation; expose owner-scoped progress, cancel, and resume | Keeping an HTTP request open or creating an untracked goroutine |
| User tracks follow-up calls for the week | Personal todos | Store ordered owner-scoped items, deadlines, and completion; batch reorder idempotently | Starting one Agent run or Scheduler definition per todo |
| Todo completion should send an email | Todo plus explicit Notification/Operation | Completion emits or invokes a separately authorized deterministic effect | Letting the todo row contain arbitrary executable callbacks |
| Team manages a shared case queue | Project Object or Workflow task | Use business-owned assignment, status, authorization, and audit | Sharing one user's personal todo list |

## Example

A portfolio analysis outlives the request, so it becomes the current user's durable background task with source conversation, progress, fenced checkpoints, cancel, resume, and terminal outcome. The user separately adds “review the analysis Friday” to an ordered private todo list; the due date is planning metadata and completing it neither resumes the Agent nor sends a notification unless a separate authorized effect is configured. A team approval belongs to Workflow Task, and “run every Friday” belongs to Scheduler, not to either personal resource.

## Permissions and scope

Both resources are owner-scoped. Operator recovery of background execution is separate from personal todo access, and neither inherits arbitrary business permissions.

## Boundaries

Background tasks own Agent execution state; todos own personal planning state; Workflow/Scheduler own business process and independent clock semantics.
