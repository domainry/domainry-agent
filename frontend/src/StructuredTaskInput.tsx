import type { StructuredInput } from './collaboration-state.ts';

export function parseStructuredInput(data:string,schema:string):StructuredInput|undefined {
 if(!data.trim()&&!schema.trim())return undefined;
 if(!data.trim())throw new Error('请填写结构化输入的 JSON 数据。');
 return {data:JSON.parse(data),...(schema.trim()?{schema:JSON.parse(schema)}:{})};
}
export function StructuredInputFields({data,schema,onData,onSchema}:{data:string;schema:string;onData:(value:string)=>void;onSchema:(value:string)=>void}) {
 return <><label>结构化输入 JSON<textarea aria-label="结构化输入 JSON" rows={5} maxLength={16384} spellCheck={false} value={data} onChange={e=>onData(e.target.value)} placeholder={'{"currency":"EUR","amount":120}'}/></label><label>输入 JSON Schema（可选）<textarea aria-label="输入 JSON Schema（可选）" rows={4} maxLength={16384} spellCheck={false} value={schema} onChange={e=>onSchema(e.target.value)} placeholder={'{"type":"object","required":["currency"]}'}/></label><p className="subtle">接单前检查数据是否符合约定格式。格式通过后，仍需核对实际内容和完成条件。</p></>;
}
