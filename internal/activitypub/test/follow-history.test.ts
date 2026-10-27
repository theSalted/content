import {test} from 'node:test';import assert from 'node:assert/strict';import {randomUUID} from 'node:crypto';
import {fixture,published,follow,actorId,remoteActor} from './helpers.ts';import {Store} from '../src/store.ts';import {FeedbackStore} from '../src/feedback.ts';import {archiveSnapshot} from '../src/contracts.ts';
const future=()=>new Date(Date.now()+600000);
async function seed(f:Awaited<ReturnType<typeof fixture>>,n=3){const events=[];for(let i=0;i<n;i++){const e=published();e.document.published_at=`2020-01-0${i+1}T00:00:00Z`;e.document.title='Archive '+i;await f.store.archive(archiveSnapshot.parse({...e,schema_version:1,type:'content.archived',source_state:'published',delivery_policy:'archive'}),'ghost');events.push(e)}return events}
const enabled=(f:Awaited<ReturnType<typeof fixture>>,limit=2)=>new Store(f.db,actorId,undefined,{limit,intervalMs:1000});
const history=(f:Awaited<ReturnType<typeof fixture>>)=>f.db.query<{id:string;history:{payload:{id:string;to:string[];cc:string[];object:{id:string;name:string;published:string;cc:string[]}}};state:string;recipient_id:string;available_at:Date;attempts:number;last_error:string|null}>('SELECT * FROM {{schema}}.outbound_intents WHERE history IS NOT NULL ORDER BY sequence');
async function accept(s:Store){const j=await s.claimIntent(future());assert.equal(j?.payload.type,'Accept');await s.handoffIntent(j!,async()=>{});}
async function undo(s:Store,x:ReturnType<typeof follow>){await s.receiveUndoFollow({id:remoteActor+'/undo/'+randomUUID(),actorId:remoteActor,followId:x.id,followActorId:remoteActor,target:actorId});}

test('history is default-off, new accepted Follow alone schedules bounded public oldest-to-newest stable IDs',async()=>{
 const f=await fixture();try{const events=await seed(f);const input=follow();await f.store.receiveFollow(input);assert.equal((await history(f)).length,0);const s=enabled(f);assert.equal((await history(f)).length,0);
 await s.receiveFollow(follow());const rows=await history(f);assert.equal(rows.length,2);assert.deepEqual(rows.map(r=>r.history.payload.object.name),['Archive 1','Archive 2']);
 for(let i=0;i<rows.length;i++){const p=rows[i]!.history.payload;assert.deepEqual(p.to,[remoteActor]);assert.deepEqual(p.cc,[]);assert.deepEqual(p.object.cc,[]);assert.equal(p.object.published,events[i+1]!.document.published_at);assert.equal((await f.db.query('SELECT id FROM {{schema}}.activities WHERE id=$1',[p.id])).length,1)}
 assert.equal((await s.claimIntent(future()))?.payload.type,'Accept');
 }finally{await f.close()}
});
test('completed history does not repeat on duplicate Follow, Undo/refollow or another follower on same server',async()=>{
 const f=await fixture();try{await seed(f,2);const s=enabled(f),input=follow();await s.receiveFollow(input);await accept(s);
 for(let i=0;i<2;i++){const j=await s.claimIntent(future());assert.ok(j?.history);await s.handoffIntent(j!,async()=>{});}
 await s.receiveFollow(input);await undo(s,input);await s.receiveFollow(follow());assert.equal((await history(f)).length,2);assert.ok((await history(f)).every(r=>r.state==='enqueued'));
 const other='https://remote.example/users/bob',id=other+'/follows/'+randomUUID();await s.receiveFollow({id,actorId:other,target:actorId,actor:{actorId:other,inbox:other+'/inbox'},follow:{id,type:'Follow',actor:other,object:actorId}});assert.equal((await history(f)).length,2);
 }finally{await f.close()}
});
test('Undo/block cancel queued history; new Follow resumes only cancelled unsent slots',async()=>{
 const f=await fixture();try{await seed(f,2);const s=enabled(f),input=follow();await s.receiveFollow(input);await accept(s);const j=await s.claimIntent(future());assert.ok(j?.history);await s.handoffIntent(j!,async()=>{});
 await undo(s,input);let rows=await history(f);assert.deepEqual(rows.map(r=>r.state),['enqueued','dead']);const ids=rows.map(r=>r.id);
 await s.receiveFollow(follow());rows=await history(f);assert.deepEqual(rows.map(r=>r.id),ids);assert.deepEqual(rows.map(r=>r.state),['enqueued','pending']);
 await new FeedbackStore(f.db).block(remoteActor,true);assert.equal((await history(f))[1]!.last_error,'actor_blocked');await s.receiveFollow(follow());assert.equal((await history(f))[1]!.state,'dead');
 }finally{await f.close()}
});
test('rate limit holds after delays; retries preserve payload and later history cannot overtake',async()=>{
 const f=await fixture();try{await seed(f,2);const s=enabled(f);await s.receiveFollow(follow());await accept(s);const now=future(),first=await s.claimIntent(now);assert.ok(first?.history);await s.failIntent(first!,now);assert.equal(await s.claimIntent(now),undefined);
 const retry=await s.claimIntent(new Date(now.getTime()+60000));assert.equal(retry?.id,first!.id);assert.deepEqual(retry?.payload,first!.payload);await s.handoffIntent(retry!,async()=>{});assert.equal(await s.claimIntent(new Date()),undefined);
 const next=await s.claimIntent(future());assert.ok(next?.history);assert.notEqual(next!.id,first!.id);
 }finally{await f.close()}
});
test('withdrawal cancels queued historical Create and newer live content keeps its ordinary route',async()=>{
 const f=await fixture();try{const [e]=await seed(f,1);const s=enabled(f);await s.receiveFollow(follow());await accept(s);const {document,...base}=e!;await s.publish({...base,event_id:randomUUID(),revision:'2',type:'content.withdrawn'},'ghost');assert.equal((await history(f))[0]!.state,'dead');
 const next=published();await s.publish(next,'ghost');const rows=await f.db.query<{history:unknown}>('SELECT history FROM {{schema}}.outbound_intents i JOIN {{schema}}.activities a ON a.id=i.activity_id WHERE a.content_id=$1',[next.content_id]);assert.equal(rows.length,1);assert.equal(rows[0]!.history,null);
 }finally{await f.close()}
});

test('an exhausted Accept cancels unsent history until a new Follow is accepted',async()=>{
 const f=await fixture();try{await seed(f,1);const s=enabled(f);await s.receiveFollow(follow());const j=await s.claimIntent(future());assert.equal(j?.payload.type,'Accept');await s.failIntent({...j!,attempts:10});assert.equal((await history(f))[0]!.last_error,'accept_failed');assert.equal(await s.claimIntent(future()),undefined);await s.receiveFollow(follow());await accept(s);assert.ok((await s.claimIntent(future()))?.history);}finally{await f.close()}
});
