// Test-only private receiver. Never imported by normal startup or runtime image.
import {createHash,timingSafeEqual} from 'node:crypto';
import {z} from 'zod';
import {makeApp} from '../src/app.ts';
import {ScopedProbeStore} from './scoped-probe-store.fixture.ts';
import {ServiceError} from '../src/contracts.ts';
import type {Federation} from '@fedify/fedify';
import type {FederationState} from '../src/federation.ts';
const bindSchema=z.strictObject({content_id:z.uuid()});
const resultSchema=z.strictObject({status:z.enum(['passed','failed']),checks:z.array(z.string().max(300)).max(50),failure:z.string().regex(/^[a-z0-9_]{1,100}$/).optional(),withdrawal_committed:z.boolean(),withdrawal_accepted:z.boolean(),withdrawal_observed:z.boolean(),undo_observed:z.boolean()});
type Result=z.infer<typeof resultSchema>;
export class VerificationSession {
 readonly state:FederationState;readonly federation:Federation<FederationState>;readonly runId:string;readonly peerActor:string;readonly prefix:string;readonly key:string[];
 contentId?:string;closed=false;private scopedFetch?:ReturnType<typeof makeApp>;readonly peerReport:()=>unknown;
 private constructor(state:FederationState,federation:Federation<FederationState>,runId:string,peerActor:string,peerReport:()=>unknown){
  this.state=state;this.federation=federation;this.runId=runId;this.peerActor=peerActor;this.peerReport=peerReport;
  this.prefix='/internal/verification/'+runId;this.key=['content','controlled-verification',runId];
 }
 static async begin(state:FederationState,federation:Federation<FederationState>,runId:string,peerActor:string,peerReport:()=>unknown){
  if(!z.uuid().safeParse(runId).success)throw new ServiceError(422,'invalid_run');
  const peer=new URL(peerActor);if(peer.protocol!=='https:'||peer.username||peer.password||peer.hash||peer.search)throw new ServiceError(422,'invalid_peer');
  const session=new VerificationSession(state,federation,runId,peerActor,peerReport);
  const rows=await state.store.db.query('INSERT INTO {{schema}}.federation_kv(key,value) VALUES($1,$2) ON CONFLICT DO NOTHING RETURNING key',[session.key,{run_id:runId,peer_actor:peerActor,phase:'reserved',started_at:new Date().toISOString()}]);
  if(!rows.length)throw new ServiceError(409,'probe_run_used');return session;
 }
 async finish(input:Result){
  const result=resultSchema.parse(input);this.closed=true;
  await this.state.store.db.query('UPDATE {{schema}}.federation_kv SET value=value||$2::jsonb WHERE key=$1',[this.key,{phase:'finished',finished_at:new Date().toISOString(),result}]);
 }
 async fetch(request:Request):Promise<Response|null>{
  const url=new URL(request.url);if(!url.pathname.startsWith(this.prefix+'/'))return null;
  if(this.closed)return new Response(null,{status:404});
  const actual=createHash('sha256').update(request.headers.get('authorization')??'').digest(),expected=createHash('sha256').update('Bearer '+this.state.config.publisherToken).digest();
  if(!timingSafeEqual(actual,expected))return Response.json({error:'unauthorized'},{status:401});
  try{
   if(url.pathname===this.prefix+'/peer'&&request.method==='GET')return Response.json(this.peerReport(),{headers:{'cache-control':'no-store'}});
   if(url.pathname===this.prefix+'/events'&&request.method==='POST'){
    if(!this.scopedFetch)throw new ServiceError(409,'probe_not_bound');
    url.pathname='/internal/v2/events';url.search='';return this.scopedFetch(new Request(url,request));
   }
   if(url.pathname!==this.prefix+'/bind')return new Response(null,{status:404});
   if(request.method!=='POST')return new Response(null,{status:405});
   if(!/^application\/json(?:;|$)/i.test(request.headers.get('content-type')??''))throw new ServiceError(415,'json_required');
   const reader=request.body?.getReader(),chunks:Uint8Array[]=[];let size=0;
   if(reader)try{for(;;){const {value,done}=await reader.read();if(done)break;size+=value.byteLength;if(size>2048){await reader.cancel();throw new ServiceError(413,'body_too_large');}chunks.push(value);}}finally{reader.releaseLock();}
   let value:unknown;try{value=JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(Buffer.concat(chunks)));}catch{throw new ServiceError(400,'invalid_json');}
   const parsed=bindSchema.safeParse(value);if(!parsed.success)throw new ServiceError(422,'invalid_binding');const id=parsed.data.content_id;
   await this.state.store.db.transaction(async tx=>{
    const [record]=await tx.query<{value:{phase:string;content_id?:string}}>('SELECT value FROM {{schema}}.federation_kv WHERE key=$1 FOR UPDATE',[this.key]);
    if(!record||!['reserved','bound'].includes(record.value.phase))throw new ServiceError(409,'probe_run_used');
    if(record.value.content_id){if(record.value.content_id!==id)throw new ServiceError(409,'probe_binding_conflict');return;}
    if((await tx.query('SELECT 1 FROM {{schema}}.content_objects WHERE content_id=$1 OR instance_id=$1',[id])).length)throw new ServiceError(409,'probe_content_used');
    await tx.query('UPDATE {{schema}}.federation_kv SET value=value||$2::jsonb WHERE key=$1',[this.key,{phase:'bound',content_id:id,bound_at:new Date().toISOString()}]);
   });
   this.contentId=id;const config=this.state.config;
   const store=new ScopedProbeStore(this.state.store.db,config.actorId,id,this.peerActor,config.publisherId,config.contentOrigins??[config.origin]);
   this.scopedFetch=makeApp({...this.state,store},this.federation);
   return Response.json({result:'bound',content_id:id},{headers:{'cache-control':'no-store'}});
  }catch(error){if(error instanceof ServiceError)return Response.json({error:error.code},{status:error.status});throw error;}
 }
}
