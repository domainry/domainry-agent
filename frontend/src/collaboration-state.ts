import type { ResultReference } from './execution-state.ts';
import type { ConversationTaskDetail } from './task-state.ts';
export type ExecutionSubject = {runtime_id:string;workspace_id:string;user_id:string};
export type DelegationParticipant = {user_id:string;operations:('view'|'communicate'|'manage'|'delivery_read'|'execution_read')[];revision?:number};
export type ModelCapabilities={context_token_limit?:number;image_input:boolean;structured_output:boolean;protocol_continuation:boolean;reasoning_efforts:string[]};
export type ModelDescriptor={key:string;identity:{provider:string;protocol:string;model:string;fingerprint:string};capabilities:ModelCapabilities;default_reasoning_effort?:string};
export type ModelSelection={key:string;reasoning_effort?:string;identity:ModelDescriptor['identity'];capabilities:ModelCapabilities};
export type ExternalAgentCapabilities = {steering:boolean;cancellation:boolean;resume:boolean;structured_output:boolean;execution_details:boolean};
export type PeerAgent = { external?:{protocol:'domainry-peer';version:'1';capabilities:ExternalAgentCapabilities}; delegation_execution?:'caller'|'owner';delegation_role_key?:string;owner_user_id?:string; shared?:boolean; shared_with_user_ids?:string[]; definition_key?: string; definition_version?: string; definition_digest?: string; id: string; name: string; description: string; instructions: string; tools: string[]; skill_keys: string[]; model_key: string; reasoning_effort?:string; enabled: boolean; max_concurrent: number; revision: number };
export type AgentSnapshot = {id:string;name:string;revision:number;owner_user_id?:string};
// Use the write contract explicitly; directory records also contain server-owned
// identity, definition versions and timestamps that must never be submitted.
export function agentWriteInput(agent:PeerAgent,canShare:boolean,execution?:'caller'|'owner'):Record<string,unknown> {
 const {name,description,instructions,tools,skill_keys,model_key,reasoning_effort,enabled,max_concurrent,definition_key,revision,external}=agent;
 return {name,description,instructions,tools,skill_keys,model_key,reasoning_effort,enabled,max_concurrent,definition_key,external,expected_revision:revision,...(canShare?{shared_with_user_ids:agent.shared_with_user_ids||[]}:{}),...(execution?{delegation_execution:execution}:{})};
}
export type AgentPage = { availability?: AgentCandidate[]; definitions?: AgentDefinition[]; skills?: SkillSummary[]; items: PeerAgent[]; tools: { key: string; description: string; effect: string }[]; models: string[]; model_catalog?:ModelDescriptor[]; complete: boolean };
export type TaskBrief = { verification_rules?: CompletionRule[]; version: number; goal: string; deliverable: string; audience: string; constraints: string[]; completion_conditions: string[]; assumptions: string[]; due_at?: string };
export type SharedDocumentReference = {library_id:string;document_id:string;revision:number;sha256:string};
export type PeerMessage = {participant_user_id?:string;participant_revision?:number;consumed_at?:string;documents?:SharedDocumentReference[];documents_omitted?:boolean;disagreement_id?:string;disagreement_revision?:number; agreement_revision?: number; delivery_mode?: string; after_run_id?: string; reply_to_id?: string; answered_question_ids?:string[]; answered_by_id?: string; superseded?: boolean; change?: RequirementChange; id: string; from_agent_id?: string; from_user_id?: string; to_agent_id: string; kind: string; content: string; brief_version: number; consumed_by_run_id?: string; created_at: string };
export type StructuredInput = {data:unknown;schema?:unknown};
export type Assignment = {execution_subject?:ExecutionSubject;number:number;agent_id:string;agent_revision:number;conversation_id:string;task_id:string;agreement_revision:number;reason:string;actor_id:string;created_at:string;previous_delivery?:Delegation["delivery"]};
export type Handoff = {remaining_work:string;runs:{conversation_id:string;run_id:string}[];effects:{tool:string;arguments:string;status:string;completion?:string;resource_id?:string;reference:ResultReference}[]};
export type WorkBudget = {max_input_tokens:number;max_output_tokens:number;max_duration_seconds:number;max_model_cost?:number;currency?:string};
export type WorkUsage = {input_tokens:number;output_tokens:number;duration_ms:number;model_calls:number;tool_calls:number;model_cost:number;currency?:string;cost_known:boolean;started_at:string};
export type CollaborationOperation = 'discover'|'configure'|'initiate'|'view'|'receive'|'manage'|'communicate'|'execution_read'|'delivery_read'|'share';
export type CollaborationAuthorization = {allowed:CollaborationOperation[]|null;revision:string};
export type CollaborationAccess = Record<Exclude<CollaborationOperation,'discover'|'configure'|'initiate'>,boolean>;
export type Delegation = {source_agent?:AgentSnapshot;subject_exited?:boolean;delivery_omitted?:boolean;contract_omitted?:boolean;owner_user_id?:string;participants_revision?:number;participants?:DelegationParticipant[];execution_subject?:ExecutionSubject;access?:CollaborationAccess;disagreements?:DisagreementSummary[];disagreements_omitted?:boolean;verification?: DeliveryVerification; assignment_number?:number;assignments?:Assignment[];handoff?:Handoff; structured_input?: StructuredInput; work_budget?:WorkBudget;work_usage?:WorkUsage; root_conversation_id?: string; dependencies?: TaskDependency[]; dependency_states?: DependencyState[]; pending_changes?: RequirementChange[]; agreement_revision?: number; adopted_agreement_revision?: number; adopted_at?: string; messages_complete?: boolean; id: string; from_agent_id: string; to_agent_id: string; source_conversation_id: string; conversation_id: string; purpose: string; brief: TaskBrief; status: string; revision: number; decision?: string; task?: ConversationTaskDetail; messages: PeerMessage[]; delivery?: { conditions?: ConditionAssessment[]; agreement_revision?: number; brief_version: number; summary: string; data?: unknown; evidence: { conversation_id: string; run_id: string }[]; unresolved: string[] } };
export const delegationLabel: Record<string,string> = { accepted: '已接单', running: '执行中', awaiting_delivery: '等待提交交付', delivered: '等待验收', accepted_delivery: '已验收', needs_changes: '需要修改', needs_update: '需求已更新，等待继续', paused: '已暂停', cancelled: '已取消', rejected: '已拒绝', failed: '执行失败' };
export function delegationNeedsAttention(d: Delegation): boolean {
 if(d.subject_exited)return false; return !!d.contract_omitted || disagreementsBlock(d) || !!d.task?.waiting || !!d.pending_changes?.length || d.messages.some(m => isOpenQuestion(m,d)) || ['awaiting_delivery', 'delivered', 'needs_changes', 'needs_update', 'failed', 'rejected'].includes(d.status); }

export type DelegationScope = 'all'|'sent'|'received'|'current'|'current_sent'|'current_received';
export function delegationInScope(d:Delegation,scope:DelegationScope,conversationID:string,userID:string):boolean {
 const sent=!d.owner_user_id||d.owner_user_id===userID;
 const received=d.execution_subject?.user_id===userID||!!d.assignments?.some(item=>item.execution_subject?.user_id===userID);
 const receivedInConversation=d.conversation_id===conversationID||!!d.assignments?.some(item=>item.conversation_id===conversationID);
 const current=d.source_conversation_id===conversationID||receivedInConversation;
 if(scope==='sent')return sent;
 if(scope==='received')return received;
 if(scope==='current')return current;
 if(scope==='current_sent')return current&&d.source_conversation_id===conversationID;
 if(scope==='current_received')return receivedInConversation;
 return true;
}

const waitingAttentionLabel:Record<string,string>={input:'待补充信息',confirmation:'待确认操作',reconciliation:'待核查外部结果'};
export function delegationAttentionReasons(d:Delegation,userID:string):string[] {
 if(d.subject_exited)return [];
 const owner=!d.owner_user_id||d.owner_user_id===userID;
 const executor=!d.execution_subject||d.execution_subject.user_id===userID;
 const reasons:string[]=[];
 if(d.contract_omitted&&(owner||executor||!!d.access?.manage))reasons.push('委派资料需恢复');
 if(d.delivery_omitted&&d.access?.delivery_read)reasons.push('交付资料需恢复');
 if(d.task?.access_error&&executor)reasons.push('执行详情读取受阻');
 if(d.task?.waiting&&executor)reasons.push(waitingAttentionLabel[d.task.waiting.kind]||'执行等待处理');
 if(d.pending_changes?.length&&executor)reasons.push(`${d.pending_changes.length} 项要求变化待核对`);
 const questions=d.messages.filter(message=>isOpenQuestion(message,d));
 if(questions.length&&d.access?.communicate)reasons.push(`${questions.length} 个问题待回复`);
 if(disagreementsBlock(d)&&d.access?.delivery_read)reasons.push('交付分歧待核对');
 if(d.status==='delivered'&&d.access?.manage&&d.access.delivery_read)reasons.push('交付待验收');
 if(d.status==='awaiting_delivery'&&executor)reasons.push('结果待提交');
 if(['needs_changes','needs_update'].includes(d.status)&&d.access?.manage)reasons.push('需按当前要求继续');
 if(d.status==='failed'&&d.access?.manage)reasons.push('执行失败待处理');
 if(d.status==='rejected'&&d.access?.manage)reasons.push('接单被拒绝待转交');
 return [...new Set(reasons)];
}

export function delegationOpenQuestions(d:Delegation):{content:string;count:number}[] {
 const grouped=new Map<string,{content:string;count:number}>();
 for(const message of d.messages){
  if(!isOpenQuestion(message,d))continue;
  const content=message.content.trim().replace(/\s+/g,' ');
  if(!content)continue;
  const key=content.toLocaleLowerCase();const previous=grouped.get(key);
  grouped.set(key,previous?{...previous,count:previous.count+1}:{content,count:1});
 }
 return [...grouped.values()];
}

export type DelegationConditionProgress={requirement:string;verdict:'met'|'unmet'|'unknown';basis:string;method:string};
export function delegationConditionProgress(d:Delegation):DelegationConditionProgress[] {
 return (d.brief.completion_conditions||[]).map((requirement,condition)=>{
  const check=d.verification?.checks?.find(item=>item.condition===condition);
  return {requirement,verdict:check?.verdict||'unknown',basis:check?.basis||'',method:check?.method||''};
 });
}

const activityStatusLabel:Record<string,string>={generating:'正在生成',tools:'正在处理工具结果',completed:'已完成',running:'执行中',receiving:'正在接收参数',queued:'等待执行',waiting_user:'等待补充信息',waiting_confirmation:'等待操作确认',uncertain:'结果待核查',failed:'失败'};
export function delegationCurrentActivity(d:Delegation):string {
 if(!d.task)return '';
 if(d.task.waiting)return `等待处理：${d.task.waiting.question}`;
 if(d.task.external_execution){const external=d.task.external_execution,last=external.events.at(-1);if(last)return `${last.phase?last.phase+' · ':''}${last.summary}`;if(!external.details_available&&external.status==='running')return '外部 Agent 正在执行；该协议未提供过程详情';return external.status==='waiting_claim'?'等待外部 Agent 认领':`外部 Agent · ${external.status}`;}
 const step=d.task.steps?.length?[...d.task.steps].sort((a,b)=>a.number-b.number).at(-1):undefined;
 const call=step?.calls?.find(item=>!['completed','failed','not_started'].includes(item.status))||step?.calls?.at(-1);
 if(step&&call)return `当前步骤 ${step.number+1} · ${call.name} · ${activityStatusLabel[call.status]||call.status}`;
 if(step)return `当前步骤 ${step.number+1} · ${activityStatusLabel[step.status]||step.status}`;
 if(['queued','running'].includes(d.task.status))return `第 ${d.task.progress.attempt||1} 次处理 · 已记录 ${d.task.progress.steps} 个步骤、${d.task.progress.tool_calls} 次工具调用`;
 return '';
}

export function delegationBusinessStage(d:Delegation):string {
 if(d.subject_exited)return '执行主体已退出，工作已停止';
 if(d.contract_omitted)return '约定资料暂不可读';
 const checks=delegationConditionProgress(d),met=checks.filter(item=>item.verdict==='met').length,unmet=checks.filter(item=>item.verdict==='unmet').length,reviewed=met+unmet;
 if(d.status==='accepted_delivery')return `交付已验收 · ${reviewed}/${checks.length} 项完成条件有核对结论`;
 if(d.status==='delivered')return unmet?`交付待处理 · ${unmet} 项条件未满足`:`交付待验收 · ${reviewed}/${checks.length} 项完成条件有核对结论`;
 if(d.status==='needs_changes')return '交付需修改';
 if(d.status==='needs_update')return '需核对新要求后继续';
 if(d.status==='awaiting_delivery')return '执行已结束，等待提交交付';
 return delegationLabel[d.status]||d.status;
}
export function delegationActions(d: Delegation): string[] {
 if(d.subject_exited)return [];
 if (!d.access?.view || !d.access.manage) return [];
 return delegationStatusActions(d).filter(action=>(!d.contract_omitted||['pause','cancel'].includes(action))&&(!['accept_delivery','review_delivery'].includes(action)||d.access?.delivery_read));
}
function delegationStatusActions(d: Delegation): string[] {
 if (d.status==='cancelled') return [];
 if (d.status==='rejected') return ['transfer'];
 if (d.status==='accepted_delivery') return ['update_brief','update_input','set_dependencies'];
 const actions = ['update_brief','update_input','set_dependencies','cancel'];
	const recoveryRequired=!!d.task&&['agent_changed','agent_disabled','model_changed'].includes(d.task.error_code||'');
	if(recoveryRequired)actions.unshift('recover');
 if(['awaiting_delivery','delivered','failed'].includes(d.status)||d.task&&['completed','failed','cancelled'].includes(d.task.status)) actions.push('transfer');
 if (!recoveryRequired) {
  if (['paused','needs_changes','needs_update','failed'].includes(d.status)) actions.unshift('resume'); else actions.unshift('pause');
 }
 if (['delivered','awaiting_delivery'].includes(d.status)) actions.unshift('request_changes');
 if(d.status==='delivered'&&deliveryIsCurrent(d)) actions.unshift('review_delivery');
 if (d.status === 'delivered' && d.task?.status === 'completed' && deliveryIsCurrent(d) && !disagreementsBlock(d)) actions.unshift('accept_delivery');
 return actions;
}
export const decisionLabel: Record<string,string> = { review_delivery: '核对交付', recover:'按当前配置重启', transfer: '转交工作', update_input: '更新输入资料', set_dependencies: '调整依赖', pause: '暂停', cancel: '取消委派', resume: '继续执行', update_brief: '更新需求', accept_delivery: '验收通过', request_changes: '要求修改' };
export function lines(value: string): string[] { return value.split('\n').map(s => s.trim()).filter(Boolean); }
export function peerPath(id: string): string { return `/agent/delegations/${encodeURIComponent(id)}`; }

export type AgentDefinition = { key: string; version: string; name: string; description: string; instructions: string; tools: string[]; skill_keys: string[] };
export type SkillSummary = { key: string; version: string; name: string; description: string; allowed_tools: string[]; input_schema?:unknown; output_schema?:unknown; resources?:{key:string;name:string;description?:string;media_type:string}[]; workflow_steps:number };
export type AgentRequirements = { task_type?: string; tools?: string[]; skills?: string[]; sources?: { conversation_id: string; run_id: string; before_step?: number }[]; input_tokens?: number; output_tokens?: number; max_model_cost?: number; currency?: string };
export type AgentCandidate = { agent_id: string; revision: number; state: string; can_accept: boolean; running: number; queued: number; waiting: number; available_slots: number; missing_tools: string[]; missing_skills: string[]; unavailable_tools: string[]; source_access: string; reasons: string[]; checked_at: string; cost: { known: boolean; amount: number; currency?: string; input_tokens: number; output_tokens: number; basis: string; uncertainty: string; price?: { basis: string; updated_at: string } }; history: { runs: number; completed_runs: number; failed_runs: number; accepted_deliveries: number; reviewed_deliveries: number; acceptance_rate_known: boolean; acceptance_rate: number; usage_samples: number; mean_duration_ms: number; mean_coordination_ms: number; mean_tool_calls: number } };
export type AgentMatches = { items: AgentCandidate[]; recommended_agent_id?: string; checked_at: string; basis: string; history_complete: boolean };
export const agentAvailabilityLabel: Record<string,string> = { ready: '可以接单', queued: '接单后排队', blocked: '暂不能接单' };
export const agentReasonLabel: Record<string,string> = { receiver_access_denied: '当前执行身份没有接单权限', agent_disabled: '已停用', agent_model_or_profile_unavailable: '模型或能力定义不可用，请更新配置', delegation_cycle: '当前会话或来源链中的 Agent，不能循环委派', source_access_denied: '所需资料无法在独立会话中读取', configured_tools_unavailable: '部分已选工具当前不可用', capabilities_missing: '缺少这项工作需要的工具或 Skill', queue_full: '当前用户或工作空间的队列已满', wait_for_capacity: '正在处理其他工作，需要等待执行名额', cost_unknown: '成本依据不足或币种不符，无法满足费用筛选', estimated_cost_exceeded: '模型预估费用超过筛选上限' };

export type DependencyInput = { delegation_id: string; brief_version: number; agreement_revision?: number; fields?: string[] };
export type TaskDependency = DependencyInput & { digest: string; values: Partial<TaskBrief> & {input?: StructuredInput} };
export type DependencyState = { delegation_id: string; current_brief_version: number; current_agreement_revision: number; state: string };
export type RequirementChange = { id: string; source_delegation_id: string; source_brief_version: number; source_agreement_revision: number; via_delegation_id?: string; changed_fields: string[]; created_at: string };
export type AgreementEntry = { structured_input?: StructuredInput; revision: number; brief: TaskBrief; dependencies: TaskDependency[]; changed_fields: string[]; reason: string; from_user_id?: string; from_agent_id?: string; created_at: string };
export type AgreementHistory = { items: AgreementEntry[]; complete: boolean; next_before?: number };
export const briefFieldLabel: Record<string,string> = { verification_rules: '完成条件检查规则', assignment: '接收方与剩余工作', input: '结构化输入', goal: '目标', deliverable: '交付物', audience: '面向谁', constraints: '约束', completion_conditions: '完成条件', assumptions: '假设', due_at: '截止时间', dependencies: '依赖要求' };
export function deliveryIsCurrent(d: Delegation): boolean { return !!d.delivery && d.delivery.brief_version===d.brief.version && (d.delivery.agreement_revision||1)===(d.agreement_revision||1) && !d.pending_changes?.length; }
export function isOpenQuestion(m: PeerMessage,d: Delegation): boolean { return m.kind==='question' && !m.answered_by_id && !m.superseded && m.brief_version===d.brief.version && (m.agreement_revision||1)===(d.agreement_revision||1) && !['cancelled','rejected','accepted_delivery'].includes(d.status); }
export function reviewedDependencies(d: Delegation): DependencyInput[] { return (d.dependencies||[]).map(edge=>{const state=d.dependency_states?.find(s=>s.delegation_id===edge.delegation_id);return {delegation_id:edge.delegation_id,brief_version:state?.current_brief_version||edge.brief_version,agreement_revision:state?.current_agreement_revision||edge.agreement_revision||1,fields:edge.fields};}); }

export type CompletionRule = {condition:number;kind:'data'|'receipt';schema?:unknown;tool?:string;arguments_schema?:unknown;result_schema?:unknown;min_receipts?:number;completion?:'completed'|'accepted'};
export type ConditionAssessment = {condition:number;verdict:'met'|'unmet'|'unknown';basis:string;receipts?:ResultReference[]};
export type CompletionCheck = ConditionAssessment & {requirement:string;method:string};
export type DeliveryVerification = {delivery_digest:string;brief_version:number;agreement_revision:number;checks:CompletionCheck[];ready:boolean;blockers:string[];actor_id:string;agent_id?:string;source?:{conversation_id:string;run_id:string};checked_at:string};
export type DeliveryPublicationReceipt = {delivery_revision:number;record_digest:string;publisher:{user_id:string;role_key:string};recipient_user_id:string;published_at:string};
export type DeliveryHistory = {items:{publication?:DeliveryPublicationReceipt;revision:number;kind:string;delivery:NonNullable<Delegation['delivery']>;verification:DeliveryVerification;reason:string}[];complete:boolean;next_before?:number};
export type DeliveryPublicationCandidates = {items:{revision:number;kind:string;brief_version:number;agreement_revision:number}[];current_available:boolean;complete:boolean;next_before?:number};
export type DeliveryPublicationPreview = {record:DeliveryHistory['items'][number];record_digest:string;expected_revision:number;publisher:{user_id:string;role_key:string};recipient_user_id:string};
export function canRepublishDelivery(d:Delegation,userID:string,canShare:boolean):boolean {
 return !!userID&&canShare&&!!d.access?.view&&!!d.access?.receive&&!!d.access?.delivery_read&&(d.execution_subject?d.execution_subject.user_id===userID:!d.owner_user_id||d.owner_user_id===userID);
}

export function initialDeliveryReview(d:Delegation):ConditionAssessment[] {
	if(d.contract_omitted)return [];
 return d.brief.completion_conditions.map((_,condition)=>{
  const check=d.verification?.checks.find(c=>c.condition===condition);
  const claim=d.delivery?.conditions?.find(c=>c.condition===condition);
  const reviewed=check&&['agent','user'].includes(check.method);
  return {condition,verdict:reviewed?check.verdict:'unknown',basis:reviewed?check.basis:'',receipts:check?.receipts||claim?.receipts};
 });
}

// Rule indexes are local to an exact brief. Preserve only unchanged conditions;
// inserting/removing/reordering text must not silently move a rule to new work.
export function withCompletionConditions(brief:TaskBrief,conditions:string[]):TaskBrief {
 const previous=brief.completion_conditions.map(c=>c.trim()),next=conditions.map(c=>c.trim());
 const verification_rules=(brief.verification_rules||[]).flatMap(rule=>{
  const text=previous[rule.condition];
  if(!text)return [];
  if(previous.filter(c=>c===text).length===1&&next.filter(c=>c===text).length===1)return [{...rule,condition:next.indexOf(text)}];
  // Duplicate conditions are ambiguous after edits and need explicit rules.
  return [];
 });
 return {...brief,completion_conditions:conditions,verification_rules};
}

export type DisagreementClaimInput={conclusion:string;data_scope:string;period:string;source_version:string;calculation:string;receipts?:ResultReference[]};
export type DisagreementActor={user_id:string;agent_id?:string;source?:{conversation_id:string;run_id:string}};
export type DisagreementSummary={id:string;revision:number;title:string;condition?:number;requirement?:string;status:string;brief_version:number;agreement_revision:number;delivery_digest:string;owner_agent_id?:string;owner_user_id?:string;next_action?:string;updated_at:string};
export type EvidenceComparison={data_scope:string;period:string;source_version:string;calculation:string};
export type DisagreementDecision={outcome:string;adopt_claim_id?:string;basis:string;comparison:EvidenceComparison;owner_agent_id?:string;next_action?:string;brief_version:number;agreement_revision:number;delivery_digest:string};
export type DisagreementRecord=DisagreementSummary&{claims:(DisagreementClaimInput&{id:string;actor:DisagreementActor;created_at:string})[];decision?:DisagreementDecision;actor:DisagreementActor;decision_actor?:DisagreementActor;event:string;reason:string};
export type DisagreementHistory={items:DisagreementRecord[];complete:boolean;next_before?:number};
export const disagreementLabel:Record<string,string>={needs_review:'需按新版本核对',open:'待核对',checking:'需要补查',needs_revision:'需要修订',waiting_user:'等待用户判断',resolved:'已采用结论'};
export function disagreementNeedsReview(issue:DisagreementSummary,d:Delegation):boolean{return issue.brief_version!==d.brief.version||issue.agreement_revision!==(d.agreement_revision||1)||issue.delivery_digest!==(d.delivery?d.verification?.delivery_digest:'');}
export function disagreementsBlock(d:Delegation):boolean{return d.status!=='cancelled'&&(!!d.disagreements_omitted||(d.disagreements||[]).some(issue=>issue.status!=='resolved'||disagreementNeedsReview(issue,d)));}

export type ContractPublicationCandidate = {revision:number;brief_version:number};
export type ContractPublicationCandidates = {items:ContractPublicationCandidate[];current_agreement_revision:number;complete:boolean;next_before?:number};
export type ContractPublicationPreview = {agreement:AgreementEntry;requirements:AgentRequirements;sources:{conversation_id:string;run_id:string;before_step?:number}[];record_digest:string;expected_revision:number;publisher:{user_id:string;role_key?:string};recipient_user_id:string};
export type ContractPublicationReceipt = {revision:number;agreement_revision:number;record_digest:string;publisher:{user_id:string;role_key?:string};recipient_user_id:string;published_at:string;reason:string};
export type ContractPublicationHistory = {items:ContractPublicationReceipt[];complete:boolean;next_before?:number};
export function canRepublishContract(d:Delegation,userID:string,canShare:boolean):boolean {
 if(d.subject_exited)return false;
 return !!userID&&canShare&&!!d.access?.view&&!!d.access?.manage&&(!d.owner_user_id||d.owner_user_id===userID);
}

export function canReadContractPublicationHistory(d:Delegation,userID:string):boolean {
 if(d.subject_exited)return false;
 return !!userID&&!!d.access?.view&&(!d.owner_user_id||d.owner_user_id===userID||d.execution_subject?.user_id===userID);
}
