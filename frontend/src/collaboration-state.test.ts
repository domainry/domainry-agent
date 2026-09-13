import test from 'node:test';
import assert from 'node:assert/strict';
import { agentWriteInput, delegationActions, delegationNeedsAttention, deliveryIsCurrent, initialDeliveryReview, withCompletionConditions, reviewedDependencies, disagreementsBlock, type Delegation, type PeerAgent } from './collaboration-state.ts';
const work = { access:{view:true,manage:true,execution_read:true,delivery_read:true,communicate:true,receive:true,share:true}, id:'d',status:'delivered',brief:{version:1},agreement_revision:2,delivery:{brief_version:1,agreement_revision:1},task:{status:'completed'},messages:[] } as unknown as Delegation;
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
