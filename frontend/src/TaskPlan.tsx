import { useEffect, useMemo, useState } from "react";
import { formatLocalDateTime } from "./time.ts";
import { Button } from "./components/ui/button";
import { request } from "./api.ts";
import { describeError } from "./errors.ts";
import { taskPlanStatusLabel, taskPlansPath, type ConversationPlan, type ConversationPlanHistory, type ConversationTaskDetail } from "./task-state.ts";

type PlanLinks = { onRun: (conversationID: string, runID: string) => void; onArtifact: (id: string, version: number) => void };

function PlanSteps({ plan, onRun, onArtifact }: { plan: ConversationPlan } & PlanLinks) {
  return <>
    <p className="subtle">{plan.reason} · {formatLocalDateTime(plan.created_at)}</p>
    <ol className="task-plan-steps">{plan.steps.map(step => <li key={step.id} className={`plan-${step.status}`}>
      <div className="task-plan-step-heading"><span className="agent-label">{taskPlanStatusLabel(step.status)}</span><strong>{step.title}</strong><small>{step.id}</small></div>
      {!!step.depends_on.length && <p><span>依赖：</span>{step.depends_on.join("、")}</p>}
      {step.input && <p><span>输入：</span>{step.input}</p>}<p><span>预期输出：</span>{step.expected_output}</p>
      <p><span>执行者：</span>{step.executor.agent_id} · {step.executor.run_id}</p>
      {!!step.requirement_fields.length && <p><span>依据字段：</span>{step.requirement_fields.join("、")}</p>}
      {step.outcome && <p><span>实际结果：</span>{step.outcome}</p>}{step.blocker && <p className="text-destructive"><span>阻塞：</span>{step.blocker}</p>}
      {(step.evidence.length > 0 || step.artifacts.length > 0) && <div className="task-plan-references">
        {step.evidence.map(reference => <Button key={`${reference.conversation_id}:${reference.run_id}:${reference.step}:${reference.call_id}`} size="sm" variant="ghost" onClick={() => onRun(reference.conversation_id, reference.run_id)}>执行证据 {reference.call_id}</Button>)}
        {step.artifacts.map(reference => <Button key={`${reference.id}:${reference.version}`} size="sm" variant="outline" onClick={() => onArtifact(reference.id, reference.version)}>成果 {reference.id} v{reference.version}</Button>)}
      </div>}
    </li>)}</ol>
  </>;
}

export function TaskPlanSnapshot({ plan, onRun, onArtifact }: { plan: ConversationPlan } & PlanLinks) {
  return <section className="task-plan" aria-label="委派执行计划">
    <div className="task-plan-heading"><div><strong>当前执行计划</strong><small>计划 v{plan.version} · 要求 v{plan.agreement_revision}</small></div></div>
    <PlanSteps plan={plan} onRun={onRun} onArtifact={onArtifact}/>
  </section>;
}

export function TaskPlan({ task, onRun, onArtifact }: { task: ConversationTaskDetail } & PlanLinks) {
  const [history, setHistory] = useState<ConversationPlanHistory>({ items: [], complete: true });
  const [selected, setSelected] = useState(task.plan?.version || 0);
  const [error, setError] = useState("");
  useEffect(() => {
    setSelected(task.plan?.version || 0); setError("");
    const controller = new AbortController();
    request<ConversationPlanHistory>(taskPlansPath(task.id), "GET", undefined, controller.signal)
      .then(value => { if (!controller.signal.aborted) setHistory(value); })
      .catch(failure => { if (!controller.signal.aborted) setError(describeError(failure)); });
    return () => controller.abort();
  }, [task.id, task.plan?.version]);
  const plans = useMemo(() => {
    const values = [...history.items];
    if (task.plan && !values.some(item => item.version === task.plan!.version)) values.unshift(task.plan);
    return values.sort((left, right) => right.version - left.version);
  }, [history, task.plan]);
  const plan: ConversationPlan | undefined = plans.find(item => item.version === selected) || task.plan;
  return <section className="task-plan" aria-label="执行计划">
    <div className="task-plan-heading"><div><strong>执行计划</strong>{plan && <small>计划 v{plan.version} · 要求 v{plan.agreement_revision}</small>}</div>{plans.length > 1 && <select aria-label="计划历史版本" value={plan?.version || 0} onChange={event => setSelected(Number(event.target.value))}>{plans.map(item => <option key={item.version} value={item.version}>计划 v{item.version}</option>)}</select>}</div>
    {error && <p role="alert" className="text-destructive">{error}</p>}
    {!plan ? <p className="subtle">这个任务尚未建立多步骤计划；简单请求可以直接执行。</p> : <>
      <PlanSteps plan={plan} onRun={onRun} onArtifact={onArtifact}/>
      {!history.complete && <small className="subtle">这里只显示最近 20 个计划版本。</small>}
    </>}
  </section>;
}
