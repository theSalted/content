import {test} from 'node:test';import assert from 'node:assert/strict';import {randomUUID} from 'node:crypto';import {MemoryKvStore} from '@fedify/fedify';
import {fixture,published,follow,remoteActor,CaptureQueue} from './helpers.ts';import {makeFederation} from '../src/federation.ts';import {splitIngress} from '../src/ingress.ts';import {VerificationSession} from './verification-session.fixture.ts';
test('verification route requires one durable fresh binding, preserves normal routes and cannot replay a run',async()=>{
 const f=await fixture();try{
  const federation=makeFederation(f.state,new MemoryKvStore(),new CaptureQueue()),run=randomUUID(),session=await VerificationSession.begin(f.state,federation,run,remoteActor,()=>({activities:[],objects:[]}));
  const req=(suffix:string,body?:unknown,token=f.config.publisherToken)=>new Request('http://127.0.0.1:3001'+session.prefix+suffix,{method:body?'POST':'GET',headers:{authorization:'Bearer '+token,'content-type':'application/json'},...(body?{body:JSON.stringify(body)}:{})});
  assert.equal((await session.fetch(req('/bind',{content_id:randomUUID()},'wrong')))?.status,401);
  assert.equal((await session.fetch(req('/events',published())))?.status,409);
  const old=published();await f.store.publish(old,'ghost');assert.equal((await session.fetch(req('/bind',{content_id:old.content_id})))?.status,409);
  const event=published();assert.equal((await session.fetch(req('/bind',{content_id:event.content_id})))?.status,200);assert.equal((await session.fetch(req('/bind',{content_id:event.content_id})))?.status,200);
  assert.equal((await session.fetch(req('/bind',{content_id:randomUUID()})))?.status,409);
  await f.store.receiveFollow(follow());assert.equal((await session.fetch(req('/events',event)))?.status,202);assert.equal((await session.fetch(req('/events',published())))?.status,403);
  assert.equal(await session.fetch(new Request('http://127.0.0.1:3001/internal/v2/events')),null);
  const split=splitIngress(async req=>await session.fetch(req)??new Response(null,{status:404}),true);assert.equal((await split.publicFetch(req('/peer'))).status,404);assert.equal((await split.privateFetch(req('/peer'))).status,200);
  await session.finish({status:'failed',checks:[],failure:'synthetic_test',withdrawal_committed:false,withdrawal_accepted:false,withdrawal_observed:false,undo_observed:false});assert.equal((await session.fetch(req('/peer')))?.status,404);
  await assert.rejects(VerificationSession.begin(f.state,federation,run,remoteActor,()=>({})),/probe_run_used/);
  const [saved]=await f.db.query<{value:{phase:string;result:{failure:string}}}>('SELECT value FROM {{schema}}.federation_kv WHERE key=$1',[session.key]);assert.equal(saved!.value.phase,'finished');assert.equal(saved!.value.result.failure,'synthetic_test');
 }finally{await f.close();}
});
