import test from "node:test";
import { updateAttachmentPage } from "./attachment-state.ts";
import assert from "node:assert/strict";
import { attachmentIndexDetail, attachmentIndexReason, indexReceipt, parsePendingAttachmentIndex, submitAttachmentIndex, verifyAttachmentIndexResponse } from "./attachment-index-state.ts";
import type { Attachment } from "./attachment-state.ts";
const attachment={id:"att_"+"a".repeat(32),conversation_id:"conv_"+"b".repeat(32),sha256:"c".repeat(64),revision:4,state:"stored",indexing:{requested:false,can_start:true,can_check:false,max_bytes:10*1024*1024}} as Attachment;
test("index receipt is bound to the exact conversation, attachment and original",()=>{
 const p=indexReceipt(attachment,"start");assert.deepEqual(parsePendingAttachmentIndex(JSON.stringify(p),attachment),p);
 for(const changed of [{conversationID:"conv_"+"d".repeat(32)},{attachmentID:"att_"+"d".repeat(32)},{sha256:"d".repeat(64)},{revision:0},{operation:"reset"}])assert.equal(parsePendingAttachmentIndex(JSON.stringify({...p,...changed}),attachment),null);
 assert.equal(parsePendingAttachmentIndex("x".repeat(2049),attachment),null);
});
test("indexing and lookup are each one scoped request without original bytes",async()=>{
 const saved=globalThis.fetch, calls:{url:string;init?:RequestInit}[]=[];
 globalThis.fetch=async(input,init)=>{calls.push({url:String(input),init});return new Response(JSON.stringify(attachment),{status:200,headers:{"Content-Type":"application/json"}});};
 try{
  for(const op of ["start","check"] as const)await submitAttachmentIndex(indexReceipt(attachment,op),new AbortController().signal);
  assert.equal(calls.length,2);
  for(const [i,c] of calls.entries()){assert.equal(c.init?.method,"POST");assert.equal(c.init?.body,undefined);assert.equal(c.url,`/agent/conversations/${attachment.conversation_id}/attachments/${attachment.id}/index${i?"/check":""}?expected_revision=4`);}
 }finally{globalThis.fetch=saved;}
});
test("index response rejects another file or missing authoritative capabilities",()=>{
 const p=indexReceipt(attachment,"check");assert.equal(verifyAttachmentIndexResponse(attachment,p),attachment);
 for(const changed of [{id:"other"},{conversation_id:"other"},{sha256:"d".repeat(64)},{revision:0},{indexing:undefined}])assert.throws(()=>verifyAttachmentIndexResponse({...attachment,...changed} as Attachment,p));
});
test("unknown writes and cleanup are not shown as retryable uploads or completion",()=>{
 assert.match(attachmentIndexDetail({...attachment,state:"failed",error_code:"attachment_put_uncertain"}),/不会自动重复上传/);
 assert.match(attachmentIndexDetail({...attachment,state:"deleting"}),/未确认前会保留/);
 assert.match(attachmentIndexReason({...attachment,indexing:{requested:false,can_start:false,can_check:false,max_bytes:10*1024*1024,reason:"attachment_size_invalid"}}),/10 MiB/);
 assert.match(attachmentIndexReason({...attachment,indexing:undefined}),/尚未开放/);
});
test("mutation response keeps the attachment list within its keyset window",()=>{
 const row=(id:string)=>({...attachment,id});
 const page={items:[row("b"),row("d")],complete:false,next_after:"d"};
 assert.deepEqual(updateAttachmentPage(page,row("a"),"a",true).items,page.items);
 assert.deepEqual(updateAttachmentPage(page,row("e"),"a",true).items,page.items);
 assert.deepEqual(updateAttachmentPage(page,row("c"),"a",true).items.map(a=>a.id),["b","c","d"]);
 const removed=updateAttachmentPage(page,{...row("d"),state:"deleting"},"a");
 assert.deepEqual(removed.items.map(a=>a.id),["b"]);assert.equal(removed.next_after,"d");
 const full={items:Array.from({length:20},(_,n)=>row(String(n).padStart(2,"0"))),complete:true};
 const inserted=updateAttachmentPage(full,row("99"),"",true);
 assert.equal(inserted.items.length,20);assert.equal(inserted.complete,false);assert.equal(inserted.next_after,"19");
});
