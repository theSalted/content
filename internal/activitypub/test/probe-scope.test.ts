import {test} from 'node:test';import assert from 'node:assert/strict';import {randomUUID} from 'node:crypto';
import {ScopedProbeStore} from './scoped-probe-store.fixture.ts';import {fixture,published,follow,remoteActor} from './helpers.ts';
test('explicit probe store fences every test lifecycle activity to its synthetic peer, preserving other followers',async()=>{
 const f=await fixture();try{
  await f.store.receiveFollow(follow());const other='https://other.example/users/existing';
  await f.store.receiveFollow({id:other+'/follow',actorId:other,target:f.config.actorId,actor:{actorId:other,inbox:other+'/inbox'},follow:{id:other+'/follow',type:'Follow',actor:other,object:f.config.actorId}});
  const event=published(),store=new ScopedProbeStore(f.db,f.config.actorId,event.content_id,remoteActor,'ghost',[f.config.origin]);
  await store.publish(event,'ghost');await store.publish({...event,event_id:randomUUID(),type:'content.updated',revision:'2'},'ghost');
  const {document,...base}=event;await store.publish({...base,event_id:randomUUID(),type:'content.withdrawn',revision:'3'},'ghost');
  const rows=await f.db.query<{recipient_id:string}>('SELECT i.recipient_id FROM {{schema}}.outbound_intents i JOIN {{schema}}.activities a ON a.id=i.activity_id AND a.publication_id=i.publication_id WHERE a.content_id=$1',[event.content_id]);
  assert.equal(rows.length,3);assert.ok(rows.every(r=>r.recipient_id===remoteActor));assert.equal((await f.store.followers(0)).length,2);
  await assert.rejects(store.publish(published(),'ghost'),/probe_scope_mismatch/);
  // Ordinary production Store still selects every follower; no fixture default.
  const normal=published();await f.store.publish(normal,'ghost');assert.equal((await f.db.query('SELECT i.recipient_id FROM {{schema}}.outbound_intents i JOIN {{schema}}.activities a ON a.id=i.activity_id AND a.publication_id=i.publication_id WHERE a.content_id=$1',[normal.content_id])).length,2);
 }finally{await f.close();}
});
