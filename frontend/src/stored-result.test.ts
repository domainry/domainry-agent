import test from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readStoredResult, type ResultSlice } from "./stored-result.ts";

const raw = JSON.stringify({status:"completed",content:{text:"真实来源\n<script>不是脚本</script>",value:"9007199254740993.20",empty:null}});
const reference = {conversation_id:"conv",run_id:"run",step:0,call_id:"call",sha256:createHash("sha256").update(raw).digest("hex")};
const total=new TextEncoder().encode(raw).length;
const page:ResultSlice={reference,json_text:raw,offset:0,next_offset:total,total_bytes:total,complete:true};
test("reconstructs UTF-8 slices and verifies the original result digest",async()=>{
 const split=raw.indexOf("真实来源");const offset=new TextEncoder().encode(raw.slice(0,split)).length;
 let calls=0;
 const text=await readStoredResult(reference,async position=>{calls++;return position===0?{...page,json_text:raw.slice(0,split),next_offset:offset,complete:false}:{...page,json_text:raw.slice(split),offset};},new AbortController().signal);
 assert.deepEqual(JSON.parse(text),JSON.parse(raw));assert.equal(calls,2);
});
test("rejects corrupt, shifted, truncated and oversized pages before exposing content",async()=>{
 for(const bad of [ {...page,reference:{...reference,run_id:"other"}}, {...page,offset:1}, {...page,next_offset:total-1}, {...page,total_bytes:3*1024*1024}, {...page,complete:false}, {...page,json_text:raw.replace("9007199254740993","9007199254740992")}, {...page,json_text:"",next_offset:0,complete:false}]){
  await assert.rejects(readStoredResult(reference,async()=>bad,new AbortController().signal));
 }
});
test("revocation and cancellation discard previously read fragments",async()=>{
 const first={...page,json_text:raw.slice(0,10),next_offset:10,complete:false};
 await assert.rejects(readStoredResult(reference,async offset=>{if(offset)throw new Error("permission revoked");return first;},new AbortController().signal),/permission revoked/);
 const abort=new AbortController();
 await assert.rejects(readStoredResult(reference,async()=>{abort.abort();return page;},abort.signal),{name:"AbortError"});
});
test("formatting retains unquoted large numbers and escaped source strings",async()=>{
 const raw='{"spec":{"filters":[9007199254740993]},"text":"braces {}, quote \\\" and slash \\\\"}';
 const reference2={...reference,sha256:createHash("sha256").update(raw).digest("hex")};
 const bytes=new TextEncoder().encode(raw).length;
 const out=await readStoredResult(reference2,async()=>({reference:reference2,json_text:raw,offset:0,next_offset:bytes,total_bytes:bytes,complete:true}),new AbortController().signal);
 assert(out.includes("9007199254740993"));assert.deepEqual(JSON.parse(out),JSON.parse(raw));
});
