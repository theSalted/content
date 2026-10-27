import {test} from 'node:test';
import assert from 'node:assert/strict';
import {randomUUID} from 'node:crypto';
import {nextUpdatedAt} from '../src/projection-time.ts';
import {fixture,published} from './helpers.ts';

test('public edit timestamps preserve newer source times and advance equal/backward/submillisecond times',()=>{
 const at='2026-10-25T12:00:00Z';
 assert.equal(nextUpdatedAt(at,undefined),at);
 assert.equal(nextUpdatedAt('2026-10-25T12:01:00Z',at),'2026-10-25T12:01:00Z');
 for(const value of [at,'2026-10-25T11:00:00Z','2026-10-25T12:00:00.000001Z'])assert.equal(nextUpdatedAt(value,at),'2026-10-25T12:00:00.001Z');
 assert.equal(nextUpdatedAt('2026-10-25T12:00:00Z','2026-10-25T13:00:00+01:00'),'2026-10-25T12:00:00.001Z');
});
test('higher revisions advance public edit clock without changing source receipts, published time or retry payload',async()=>{
 const f=await fixture();
 try{
  const initial=published();const result=await f.store.publish(initial,'ghost');assert.ok(result.object_id);
  const edit={...initial,event_id:randomUUID(),revision:'2',type:'content.updated' as const,document:{...initial.document,title:'Equal time edit'}};
  await f.store.publish(edit,'ghost');
  const before=await f.store.object(result.object_id);
  assert.equal(before?.payload?.updated,'2026-10-25T12:00:00.001Z');assert.equal(before?.payload?.published,initial.document.published_at);
  const activity=(await f.db.query<{payload:Record<string,any>}>("SELECT payload FROM {{schema}}.activities WHERE kind='Update'"))[0]!.payload;
  assert.equal(activity.published,edit.occurred_at);assert.equal(activity.object.updated,before?.payload?.updated);
  assert.equal((await f.store.publish(edit,'ghost')).result,'duplicate');assert.deepEqual((await f.store.object(result.object_id))?.payload,before?.payload);
  await f.store.publish({...edit,event_id:randomUUID(),revision:'3',occurred_at:'2026-10-25T11:00:00Z'},'ghost');
  assert.equal((await f.store.object(result.object_id))?.payload?.updated,'2026-10-25T12:00:00.002Z');
 }finally{await f.close();}
});
