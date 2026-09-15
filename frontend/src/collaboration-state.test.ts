import test from 'node:test';
import assert from 'node:assert/strict';
import { canReadContractPublicationHistory, canRepublishContract, canRepublishDelivery, agentWriteInput, delegationActions, delegationAttentionReasons, delegationBusinessStage, delegationConditionProgress, delegationCurrentActivity, delegationInScope, delegationNeedsAttention, delegationOpenQuestions, deliveryIsCurrent, initialDeliveryReview, withCompletionConditions, reviewedDependencies, disagreementsBlock, type Delegation, type PeerAgent } from './collaboration-state.ts';
const work = { access:{view:true,manage:true,execution_read:true,delivery_read:true,communicate:true,receive:true,share:true}, id:'d',status:'delivered',brief:{version:1},agreement_revision:2,delivery:{brief_version:1,agreement_revision:1},task:{status:'completed'},messages:[] } as unknown as Delegation;
test('subject exit cannot offer resume or republication from retained metadata',()=>{
 const exited={...work,subject_exited:true,contract_omitted:true,owner_user_id:'issuer',status:'cancelled'};
 assert.deepEqual(delegationActions(exited),[]);
 assert.equal(delegationNeedsAttention(exited),false);
 assert.equal(canRepublishContract(exited,'issuer',true),false);
 assert.equal(canReadContractPublicationHistory(exited,'issuer'),false);
});
test('explicit historical publication belongs to the execution user with current share access',()=>{
 const history={...work,status:'accepted_delivery',owner_user_id:'issuer',execution_subject:{runtime_id:'r',workspace_id:'w',user_id:'executor',role_key:'bound'},delivery:undefined};
 assert.equal(canRepublishDelivery(history,'executor',true),true);
 assert.equal(canRepublishDelivery(history,'issuer',true),false);
 assert.equal(canRepublishDelivery(history,'participant',true),false);
 assert.equal(canRepublishDelivery(history,'executor',false),false);
 assert.equal(canRepublishDelivery({...history,access:{...history.access!,receive:false}},'executor',true),false);
 assert.equal(canRepublishDelivery({...history,access:{...history.access!,delivery_read:false}},'executor',true),false);
 assert.equal(canRepublishDelivery({...work,owner_user_id:'owner'},'owner',true),true);
 assert.equal(canRepublishDelivery({...work,owner_user_id:'owner'},'other',true),false);
});
test('agent editor submits only write fields and preserves sharing when sharing access is absent',()=>{
 const response={id:'peer',owner_user_id:'owner',shared:false,shared_with_user_ids:['recipient'],revision:3,created_at:'server timestamp',updated_at:'server timestamp',definition_version:'v1',definition_digest:'server digest',name:'Review',description:'Review work',instructions:'Use current permissions',tools:['time_now'],skill_keys:[],model_key:'default',enabled:true,max_concurrent:1};
 const write=agentWriteInput(response as PeerAgent,true);
 for(const key of ['id','owner_user_id','shared','created_at','updated_at','revision','definition_version','definition_digest'])assert.equal(Object.hasOwn(write,key),false,key);
 assert.equal(write.expected_revision,3);
 assert.deepEqual(write.shared_with_user_ids,['recipient']);
 assert.equal(Object.hasOwn(agentWriteInput(response,false),'shared_with_user_ids'),false);
 assert.deepEqual(agentWriteInput({...response,shared_with_user_ids:[]},true).shared_with_user_ids,[]);
});
test('editing ordinary configuration preserves the saved execution role unless explicitly rebound',()=>{
 const agent={delegation_execution:'owner',delegation_role_key:'saved-finance-role',owner_user_id:'owner',revision:4} as PeerAgent;
 const ordinary=agentWriteInput(agent,true);
 assert.equal(Object.hasOwn(ordinary,'delegation_execution'),false);
 assert.equal(Object.hasOwn(ordinary,'delegation_role_key'),false);
 assert.equal(agentWriteInput(agent,true,'owner').delegation_execution,'owner');
 assert.equal(agentWriteInput(agent,false,'caller').delegation_execution,'caller');
 assert.equal(Object.hasOwn(agentWriteInput(agent,true,'owner'),'delegation_role_key'),false);
});
test('same brief does not make an old dependency agreement acceptable',()=>{
 assert.equal(deliveryIsCurrent(work),false);assert.equal(delegationActions(work).includes('accept_delivery'),false);
 const current={...work,delivery:{...work.delivery!,agreement_revision:2}};
 assert.equal(delegationActions(current).includes('accept_delivery'),true);
 assert.equal(deliveryIsCurrent({...current,pending_changes:[{} as never]}),false);
});
test('acceptance form keeps receipt evidence but does not turn recipient claims into reviewer judgments',()=>{
 const receipt={conversation_id:'c',run_id:'r',step:0,call_id:'one',sha256:'f'.repeat(64)};
 const d={...work,brief:{version:1,completion_conditions:['business check','data check']},delivery:{...work.delivery!,conditions:[{condition:0,verdict:'met',basis:'recipient claim',receipts:[receipt]}]},verification:{checks:[{condition:0,method:'recipient',verdict:'met',basis:'recipient claim',receipts:[receipt]},{condition:1,method:'program',verdict:'met',basis:'schema passes'}]}} as Delegation;
 assert.deepEqual(initialDeliveryReview(d),[{condition:0,verdict:'unknown',basis:'',receipts:[receipt]},{condition:1,verdict:'unknown',basis:'',receipts:undefined}]);
 const reviewed={...d,verification:{...d.verification!,checks:[{condition:0,requirement:'business check',method:'user',verdict:'unmet',basis:'Missing actual evidence',receipts:[receipt]}]}} as Delegation;
 assert.equal(initialDeliveryReview(reviewed)[0].verdict,'unmet');
 assert.equal(initialDeliveryReview(reviewed)[0].basis,'Missing actual evidence');
});
test('question attention closes on reply or requirement replacement',()=>{
 const question={id:'q',kind:'question',brief_version:1,agreement_revision:2};
 const pending={...work,status:'running',messages:[question]} as Delegation;
 assert.equal(delegationNeedsAttention(pending),true);
 assert.equal(delegationNeedsAttention({...pending,messages:[{...question,answered_by_id:'r'}] as never}),false);
 assert.equal(delegationNeedsAttention({...pending,agreement_revision:3}),false);
});
test('resume preserves selected fields and pins reviewed upstream agreement',()=>{
 const d={...work,dependencies:[{delegation_id:'upstream',brief_version:1,agreement_revision:1,fields:['constraints']}],dependency_states:[{delegation_id:'upstream',current_brief_version:2,current_agreement_revision:3,state:'changed'}]} as Delegation;
 assert.deepEqual(reviewedDependencies(d),[{delegation_id:'upstream',brief_version:2,agreement_revision:3,fields:['constraints']}]);
 assert.deepEqual(delegationActions({...work,status:'accepted_delivery'}),['update_brief','update_input','set_dependencies']);
});

test('condition edits never silently associate an old check with different work',()=>{
 const brief={version:1,goal:'work',deliverable:'report',completion_conditions:['Read receipt','Check output'],verification_rules:[{condition:0,kind:'receipt',tool:'read'},{condition:1,kind:'data',schema:{type:'object'}}]} as Delegation['brief'];
 assert.deepEqual(withCompletionConditions(brief,['Check output','New requirement','Read receipt']).verification_rules?.map(r=>[r.kind,r.condition]),[['receipt',2],['data',0]]);
 assert.deepEqual(withCompletionConditions(brief,['Changed receipt','Check output']).verification_rules?.map(r=>r.kind),['data']);
 assert.equal(withCompletionConditions(brief,['Read receipt','Read receipt']).verification_rules?.length,0);
 assert.equal(withCompletionConditions(brief,['Read receipt']).verification_rules?.length,1);
});

test('acceptance requires current resolved disagreements, including after delivery replacement or lost evidence access',()=>{
 const current={...work,delivery:{...work.delivery!,agreement_revision:2},verification:{delivery_digest:'receipt-v1'},disagreements:[{id:'issue',status:'resolved',brief_version:1,agreement_revision:2,delivery_digest:'receipt-v1'}]} as Delegation;
 assert.equal(disagreementsBlock(current),false);
 assert.equal(delegationActions(current).includes('accept_delivery'),true);
 for(const changed of [
  {...current,disagreements:current.disagreements!.map(i=>({...i,status:'open'}))},
  {...current,verification:{...current.verification!,delivery_digest:'receipt-v2'}},
  {...current,disagreements:[],disagreements_omitted:true},
  {...current,agreement_revision:3},
 ]) {
  assert.equal(disagreementsBlock(changed),true);
  assert.equal(delegationActions(changed).includes('accept_delivery'),false);
  assert.equal(delegationNeedsAttention({...changed,status:'running'}),true);
 }
});

test('collaboration controls require separate current permissions',()=>{
 const d={...work,delivery:{...work.delivery!,agreement_revision:2}};
 assert.deepEqual(delegationActions({...d,access:undefined}),[]);
 assert.deepEqual(delegationActions({...d,access:{...d.access!,manage:false}}),[]);
 const manager={...d,access:{...d.access!,delivery_read:false,execution_read:false}};
 assert.ok(delegationActions(manager).includes('pause'));
 assert.equal(delegationActions(manager).includes('accept_delivery'),false);
 assert.equal(delegationActions(manager).includes('review_delivery'),false);
});

test('management can transfer finished shared work without private task details',()=>{
 const shared={...work,task:undefined,status:'awaiting_delivery',access:{...work.access!,view:true,manage:true,execution_read:false}} as Delegation;
 assert.equal(delegationActions(shared).includes('transfer'),true);
 assert.equal(delegationActions({...shared,status:'running'}).includes('transfer'),false);
 assert.equal(delegationActions({...shared,access:{...shared.access!,manage:false}}).includes('transfer'),false);
 assert.equal(delegationActions({...shared,contract_omitted:true}).includes('transfer'),false);
});

test('changed or disabled Agent offers immutable recovery instead of retrying the stale task',()=>{
 for(const error_code of ['agent_changed','agent_disabled','model_changed']){
  const failed={...work,status:'failed',task:{status:'failed',error_code}} as Delegation;
  const actions=delegationActions(failed);
  assert.equal(actions.includes('recover'),true,error_code);
  assert.equal(actions.includes('resume'),false,error_code);
  assert.equal(actions.includes('transfer'),true,error_code);
 }
 assert.equal(delegationActions({...work,status:'failed',task:{status:'failed',error_code:'provider_failed'}} as Delegation).includes('resume'),true);
});

test('contract recovery is source-owned and requires current manage and share',()=>{
 const old={...work,contract_omitted:true,status:'accepted_delivery',owner_user_id:'issuer',execution_subject:{runtime_id:'r',workspace_id:'w',user_id:'executor'}};
 assert.equal(canRepublishContract(old,'issuer',true),true);
 assert.equal(canRepublishContract(old,'executor',true),false);
 assert.equal(canRepublishContract(old,'participant',true),false);
 assert.equal(canRepublishContract(old,'issuer',false),false);
 assert.equal(canRepublishContract({...old,access:{...old.access!,manage:false}},'issuer',true),false);
 assert.equal(canRepublishContract({...old,access:{...old.access!,view:false}},'issuer',true),false);
 assert.equal(delegationNeedsAttention(old),true);
});

test('unreadable contract keeps stopping controls without adopting hidden requirements',()=>{
 const omitted={...work,contract_omitted:true,status:'running'};
 assert.deepEqual(delegationActions(omitted),['pause','cancel']);
 assert.deepEqual(delegationActions({...omitted,status:'paused'}),['cancel']);
 assert.deepEqual(delegationActions({...omitted,status:'needs_update'}),['cancel']);
 for(const status of ['cancelled','rejected','accepted_delivery'])assert.deepEqual(delegationActions({...omitted,status}),[]);
 assert.deepEqual(delegationActions({...omitted,access:{...omitted.access!,manage:false}}),[]);
 assert.deepEqual(delegationActions({...omitted,access:{...omitted.access!,view:false}}),[]);
});

test('stopping unreadable work never initializes an assessment from its unreadable brief',()=>{
 const omitted={...work,contract_omitted:true,brief:{version:0,completion_conditions:null}} as unknown as Delegation;
 assert.deepEqual(initialDeliveryReview(omitted),[]);
});
test('contract publication history remains scoped to actual issuer and executor',()=>{
 const old={...work,owner_user_id:'issuer',execution_subject:{runtime_id:'r',workspace_id:'w',user_id:'executor'}};
 assert.equal(canReadContractPublicationHistory(old,'issuer'),true);
 assert.equal(canReadContractPublicationHistory(old,'executor'),true);
 assert.equal(canReadContractPublicationHistory(old,'participant'),false);
 assert.equal(canReadContractPublicationHistory({...old,access:{...old.access!,view:false}},'issuer'),false);
 assert.equal(canReadContractPublicationHistory({...work,owner_user_id:'issuer'},'other'),false);
});

test('global sent and received scopes use actual ownership instead of the current conversation',()=>{
 const d={...work,owner_user_id:'issuer',execution_subject:{runtime_id:'r',workspace_id:'w',user_id:'executor'},source_conversation_id:'source',conversation_id:'receiver'} as Delegation;
 assert.equal(delegationInScope(d,'sent','other','issuer'),true);
 assert.equal(delegationInScope(d,'sent','source','executor'),false);
 assert.equal(delegationInScope(d,'received','other','executor'),true);
 assert.equal(delegationInScope(d,'current_sent','source','issuer'),true);
 assert.equal(delegationInScope(d,'current_received','receiver','executor'),true);
 assert.equal(delegationInScope(d,'current','unrelated','issuer'),false);
 const transferred={...d,execution_subject:{runtime_id:'r',workspace_id:'w',user_id:'next'},conversation_id:'next-conversation',assignments:[{number:1,agent_id:'target',agent_revision:1,conversation_id:'receiver',task_id:'task',agreement_revision:1,reason:'first',actor_id:'issuer',created_at:'',execution_subject:{runtime_id:'r',workspace_id:'w',user_id:'executor'}}]} as Delegation;
 assert.equal(delegationInScope(transferred,'received','other','executor'),true);
 assert.equal(delegationInScope(transferred,'current_received','receiver','executor'),true);
});

test('attention belongs to the user who can perform the next action',()=>{
 const waiting={...work,status:'running',owner_user_id:'issuer',execution_subject:{runtime_id:'r',workspace_id:'w',user_id:'executor'},task:{...work.task!,waiting:{kind:'confirmation'}},messages:[]} as unknown as Delegation;
 assert.deepEqual(delegationAttentionReasons(waiting,'issuer'),[]);
 assert.deepEqual(delegationAttentionReasons(waiting,'executor'),['待确认操作']);
 const delivered={...waiting,status:'delivered',task:undefined,verification:{checks:[]}} as unknown as Delegation;
 assert.deepEqual(delegationAttentionReasons(delivered,'issuer'),['交付待验收']);
 assert.deepEqual(delegationAttentionReasons(delivered,'executor'),['交付待验收']);
});

test('open questions are merged while completion evidence stays itemized',()=>{
 const question={id:'q',kind:'question',to_agent_id:'source',brief_version:1,agreement_revision:2,content:' Which period? ',created_at:''} as Delegation['messages'][number];
 const d={...work,owner_user_id:'issuer',from_agent_id:'source',to_agent_id:'target',brief:{version:1,completion_conditions:['Check period','Check amount']},messages:[question,{...question,id:'q2',content:'Which   period?'}],verification:{checks:[{condition:0,verdict:'met',basis:'receipt',method:'program'}]}} as unknown as Delegation;
 assert.deepEqual(delegationOpenQuestions(d),[{content:'Which period?',count:2}]);
 assert.deepEqual(delegationConditionProgress(d),[
  {requirement:'Check period',verdict:'met',basis:'receipt',method:'program'},
  {requirement:'Check amount',verdict:'unknown',basis:'',method:''},
 ]);
 assert.match(delegationBusinessStage({...d,status:'delivered'}),/1\/2/);
});

test('current activity reports the live step without treating call counts as completion',()=>{
 const d={...work,status:'running',task:{...work.task!,status:'running',steps:[{number:2,attempt:1,status:'generating',text:'',calls:[{id:'call',name:'report_query',arguments:'{}',status:'running'}]}]}} as unknown as Delegation;
 assert.equal(delegationCurrentActivity(d),'当前步骤 3 · report_query · 执行中');
 assert.equal(delegationCurrentActivity({...d,task:{...d.task!,waiting:{kind:'reconciliation',question:'请核对是否已受理'}} as never}),'等待处理：请核对是否已受理');
});

test('external activity exposes reported progress and says when the protocol has no details',()=>{
 const base={...work,status:'running',task:{...work.task!,status:'running',external_execution:{status:'running',details_available:false,events:[]}}} as unknown as Delegation;
 assert.equal(delegationCurrentActivity(base),'外部 Agent 正在执行；该协议未提供过程详情');
 const reported={...base,task:{...base.task!,external_execution:{...base.task!.external_execution!,details_available:true,events:[{seq:1,attempt:1,kind:'progress',phase:'checking',summary:'核对金额',created_at:''}]}}} as Delegation;
 assert.equal(delegationCurrentActivity(reported),'checking · 核对金额');
});
