# Committed tool evidence in Workflow completion callbacks

Task tools append their fenced invocation ledger while the model runner is active. `SaveRunning` merges that ledger into the durable terminal run, but returns no updated value. The worker's local terminal value can therefore contain no tools even though the committed run contains them. Passing that value to `CompleteWorkflowTask` loses real evidence at the Runtime boundary.

`notifyWorkflow` now loads the committed terminal run by its exact Workspace and Task ID before invoking the host. It checks the persisted process, node, task key and version against the expected callback binding. A missing, nonterminal, foreign or unreadable run is not delivered or acknowledged. Successful, failed, cancelled and reconciled completions share this path. Host acknowledgement still precedes `MarkWorkflowCompletionDelivered`; failures retain the existing durable reconciliation path.

Regression tests reproduce a stale local snapshot with a committed tool reference/hash, and verify that the callback receives the committed ledger. Negative tests cover a missing run, read failure, nonterminal state and foreign Workspace/process/node/task bindings. No SDK or storage contract changes, permission expansion, compatibility layer or backfill is introduced.
