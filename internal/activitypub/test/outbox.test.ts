import { test,before,after,beforeEach } from 'node:test';
import assert from 'node:assert/strict';
import { fixture,follow,published } from './helpers.ts';
import { relayOne } from '../src/worker.ts';
import { randomUUID } from 'node:crypto';

let f:Awaited<ReturnType<typeof fixture>>;
before(async()=>{f=await fixture();});after(async()=>{await f.close();});beforeEach(async()=>{await f.reset();});

test('failed handoff backs off and is retried with unchanged activity ID',async()=>{
  await f.store.receiveFollow(follow());const now=new Date(Date.now()+1000);
  let id:string|undefined;
  await relayOne(f.store,async job=>{id=job.activity_id;throw new Error('temporary');},now);
  assert.equal((await f.db.query('SELECT state FROM {{schema}}.outbound_intents'))[0]!.state,'pending');
  assert.equal(await relayOne(f.store,async()=>assert.fail('backoff ignored'),now),false);
  await relayOne(f.store,async job=>assert.equal(job.activity_id,id),new Date(now.getTime()+3000));
  assert.equal((await f.db.query('SELECT state FROM {{schema}}.outbound_intents'))[0]!.state,'enqueued');
});
test('crashed worker lease can be reclaimed; stale owner cannot enqueue',async()=>{
  await f.store.receiveFollow(follow());const now=new Date(Date.now()+1000);
  const old=await f.store.claimIntent(now);assert.ok(old);
  assert.equal(await f.store.claimIntent(now),undefined);
  const reclaimed=await f.store.claimIntent(new Date(now.getTime()+61_000));assert.ok(reclaimed);
  assert.equal(reclaimed.id,old.id);assert.notEqual(reclaimed.lease_token,old.lease_token);
  assert.equal(await f.store.handoffIntent(old,async()=>assert.fail('stale lease enqueued')),false);
  assert.equal(await f.store.handoffIntent(reclaimed,async()=>{}),true);
});
test('retry exhaustion is visible as a dead intent',async()=>{
  await f.store.receiveFollow(follow());let now=new Date(Date.now()+1000);
  for(let i=0;i<10;i++){await relayOne(f.store,async()=>{throw new Error();},now);now=new Date(now.getTime()+4_000_000);}
  const row=(await f.db.query('SELECT state,attempts,last_error FROM {{schema}}.outbound_intents'))[0]!;
  assert.equal(row.state,'dead');assert.equal(row.attempts,10);assert.equal(row.last_error,'enqueue_failed');
});
test('same-object Update cannot overtake a pending Create',async()=>{
  await f.store.receiveFollow(follow());await relayOne(f.store,async()=>{},new Date(Date.now()+1000));
  const event=published();await f.store.publish(event,'ghost');
  await f.store.publish({...event,event_id:randomUUID(),revision:'2',type:'content.updated'},'ghost');
  const create=await f.store.claimIntent(new Date(Date.now()+1000));assert.ok(create);assert.equal(create.payload.type,'Create');
  assert.equal(await f.store.claimIntent(new Date(Date.now()+1000)),undefined);
  await f.store.handoffIntent(create,async()=>{});
  assert.equal((await f.store.claimIntent(new Date(Date.now()+1000)))?.payload.type,'Update');
});
test('withdrawal cancels a claimed but not handed-off stale Create',async()=>{
  await f.store.receiveFollow(follow());await relayOne(f.store,async()=>{},new Date(Date.now()+1000));
  const event=published();await f.store.publish(event,'ghost');
  const job=await f.store.claimIntent(new Date(Date.now()+1000));assert.ok(job);
  await f.store.publish({schema_version:2,publication_id:'publication-example',event_id:randomUUID(),content_id:event.content_id,object_instance:event.object_instance,delivery_policy:'live',source:event.source,revision:'2',occurred_at:event.occurred_at,type:'content.withdrawn'},'ghost');
  assert.equal(await f.store.handoffIntent(job,async()=>assert.fail('withdrawn content was sent')),false);
  assert.equal((await f.store.claimIntent(new Date(Date.now()+1000)))?.payload.type,'Delete');
});
