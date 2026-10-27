import { test,before,after,beforeEach } from 'node:test';
import assert from 'node:assert/strict';
import { Temporal } from 'temporal-polyfill';
import { fixture } from './helpers.ts';
import { DurableQueue } from '../src/queue.ts';
let f:Awaited<ReturnType<typeof fixture>>;
let q:DurableQueue;
before(async()=>{f=await fixture();q=new DurableQueue(f.db);});
after(async()=>f.close());beforeEach(async()=>f.reset());
test('transport acknowledges after handling and retains failed messages',async()=>{
  await q.enqueue({id:'a'});
  await q.processOne(async()=>{throw new Error('synthetic error');});
  assert.equal((await f.db.query('SELECT attempts FROM {{schema}}.transport_jobs'))[0]!.attempts,1);
  assert.equal(await q.processOne(()=>assert.fail('delay ignored')),false);
  await f.db.query('UPDATE {{schema}}.transport_jobs SET available_at=now()');
  await q.processOne(message=>assert.deepEqual(message,{id:'a'}));
  assert.equal((await q.getDepth()).queued,0);
});
test('explicit rescheduling retains position and blocks later messages for the same key',async()=>{
  await q.enqueue({id:'create'},{orderingKey:'object+recipient'});
  await q.enqueue({id:'update'},{orderingKey:'object+recipient'});
  await q.processOne(async message=>q.enqueue(message,{orderingKey:'object+recipient',delay:Temporal.Duration.from({minutes:1})}));
  assert.equal((await q.getDepth()).queued,2);
  assert.equal(await q.processOne(()=>assert.fail('Update overtook delayed Create')),false);
  await f.db.query('UPDATE {{schema}}.transport_jobs SET available_at=now()');
  const seen:unknown[]=[];await q.processOne(message=>{seen.push(message);});await q.processOne(message=>{seen.push(message);});
  assert.deepEqual(seen,[{id:'create'},{id:'update'}]);
});
test('poison transport messages become inspectable dead jobs after bounded retries',async()=>{
  await q.enqueue({id:'poison'});
  for(let n=0;n<12;n++){await q.processOne(()=>{throw new Error('synthetic');});await f.db.query('UPDATE {{schema}}.transport_jobs SET available_at=now()');}
  assert.equal((await f.db.query('SELECT dead FROM {{schema}}.transport_jobs'))[0]!.dead,true);
  assert.equal(await q.processOne(()=>assert.fail('dead message retried')),false);
});
