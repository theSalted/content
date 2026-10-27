import { test,before,after,beforeEach } from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { fixture,published,follow,remoteActor,actorId } from './helpers.ts';
import { loadIdentity } from '../src/identity.ts';
import { migrateSchema } from '../src/migrate.ts';
import { importExistingIdentity } from '../src/import-identity.ts';
import type { PublishingEvent } from '../src/contracts.ts';

let f:Awaited<ReturnType<typeof fixture>>;
before(async()=>{f=await fixture();});after(async()=>{await f.close();});beforeEach(async()=>{await f.reset();});
const rejection=(code:string)=>(e:unknown)=>e instanceof Error&&e.message===code;

test('schema migration is repeatable without identity replacement',async()=>{
  await migrateSchema(f.db);
  assert.equal((await loadIdentity(f.db,f.config)).fingerprint,f.state.identity.fingerprint);
});
test('missing identity fails closed and creates no keys',async()=>{
  await f.db.query('DELETE FROM {{schema}}.identities');
  await assert.rejects(loadIdentity(f.db,f.config),/Existing identity is missing/);
  assert.equal((await f.db.query('SELECT * FROM {{schema}}.identities')).length,0);
});
test('explicit identity import verifies fingerprint and never replaces existing keys',async()=>{
  const bundle={actor_id:actorId,key_id:f.config.keyId,public_jwk:f.publicJwk,private_jwk:f.privateJwk};
  await assert.rejects(importExistingIdentity(f.db,f.config,bundle),/duplicate key/);
  await f.db.query('DELETE FROM {{schema}}.identities');
  await assert.rejects(importExistingIdentity(f.db,{...f.config,expectedFingerprint:'0'.repeat(64)},bundle),/fingerprint/);
  assert.equal((await f.db.query('SELECT * FROM {{schema}}.identities')).length,0);
  const result=await importExistingIdentity(f.db,f.config,bundle);assert.equal(result.fingerprint,f.config.expectedFingerprint);
});
test('a different fingerprint or mismatched key pair cannot start',async()=>{
  await assert.rejects(loadIdentity(f.db,{...f.config,expectedFingerprint:'0'.repeat(64)}),/fingerprint mismatch/);
  const other=await crypto.subtle.generateKey({name:'RSASSA-PKCS1-v1_5',modulusLength:2048,publicExponent:new Uint8Array([1,0,1]),hash:'SHA-256'},true,['sign','verify']);
  await f.db.query('UPDATE {{schema}}.identities SET private_jwk=$1',[await crypto.subtle.exportKey('jwk',other.privateKey)]);
  await assert.rejects(loadIdentity(f.db,f.config),/key mismatch/);
});
test('publish persists object, Create and recipient intent atomically',async()=>{
  await f.store.receiveFollow(follow());
  const event=published();await f.store.publish(event,'ghost');
  const objects=await f.db.query('SELECT * FROM {{schema}}.content_objects');assert.equal(objects.length,1);
  const acts=await f.db.query('SELECT * FROM {{schema}}.activities WHERE public');assert.equal(acts.length,1);assert.equal(acts[0]!.kind,'Create');
  assert.equal((await f.db.query("SELECT * FROM {{schema}}.outbound_intents WHERE activity_id=$1",[acts[0]!.id])).length,1);
});
test('exact event replay is harmless but changing an event ID payload conflicts',async()=>{
  const event=published();await f.store.publish(event,'ghost');
  assert.equal((await f.store.publish(event,'ghost')).result,'duplicate');
  await assert.rejects(f.store.publish({...event,revision:'2'},'ghost'),rejection('event_id_conflict'));
  assert.equal((await f.db.query('SELECT * FROM {{schema}}.activities')).length,1);
});
test('old revisions cannot overwrite newer edits; equal revisions conflict',async()=>{
  const event=published();await f.store.publish(event,'ghost');
  await f.store.publish({...event,event_id:randomUUID(),revision:'3',type:'content.updated'},'ghost');
  assert.equal((await f.store.publish({...event,event_id:randomUUID(),revision:'2'},'ghost')).result,'stale');
  await assert.rejects(f.store.publish({...event,event_id:randomUUID(),revision:'3'},'ghost'),rejection('revision_conflict'));
  assert.equal(Number((await f.db.query('SELECT revision FROM {{schema}}.content_objects'))[0]!.revision),3);
});
test('slug and CMS ownership changes retain object ID and original published time',async()=>{
  const event=published();const first=await f.store.publish(event,'ghost');
  assert.ok('document'in event);
  await f.store.transferAuthority(event.content_id,'ghost','1','cms');
  await assert.rejects(f.store.publish({...event,event_id:randomUUID(),revision:'2'},'ghost'),rejection('source_not_authoritative'));
  const next={...event,event_id:randomUUID(),source:{id:'cms',epoch:'2'},revision:'2',type:'content.updated' as const,
    document:{...event.document,url:'https://federation.example/blog/new-slug',published_at:'2026-11-01T00:00:00Z'}};
  const result=await f.store.publish(next,'cms');assert.equal(result.object_id,first.object_id);
  const object=await f.store.object(result.object_id!);assert.equal(object?.payload?.published,event.document.published_at);
});
test('publisher cannot seize content by incrementing its authority generation',async()=>{
  const event=published();await f.store.publish(event,'ghost');
  await assert.rejects(f.store.publish({...event,event_id:randomUUID(),revision:'2',source:{id:'cms',epoch:'2'}},'cms'),rejection('source_not_authoritative'));
  await assert.rejects(f.store.publish(event,'cms'),rejection('publisher_source_mismatch'));
});
test('withdrawal creates Delete, hides public history and cancels unsent stale posts',async()=>{
  await f.store.receiveFollow(follow());const event=published();await f.store.publish(event,'ghost');
  const withdrawn={schema_version:2,publication_id:'publication-example',event_id:randomUUID(),content_id:event.content_id,object_instance:event.object_instance,delivery_policy:'live',source:event.source,revision:'2',
    occurred_at:'2026-10-25T13:00:00Z',type:'content.withdrawn'} as PublishingEvent;
  await f.store.publish(withdrawn,'ghost');
  const object=(await f.db.query('SELECT * FROM {{schema}}.content_objects'))[0]!;assert.equal(object.payload,null);assert.equal(object.state,'withdrawn');
  const visible=await f.store.outbox(0);assert.deepEqual(visible,[]);
  assert.equal((await f.db.query("SELECT * FROM {{schema}}.outbound_intents WHERE last_error='content_withdrawn'")).length,1);
  await assert.rejects(f.store.publish({...event,event_id:randomUUID(),revision:'3'},'ghost'),rejection('tombstone_is_final'));
});
test('Delete received before an old publish prevents resurrection',async()=>{
  const event=published();const deletion={...event,type:'content.deleted',revision:'3'} as Record<string,unknown>;delete deletion.document;
  await f.store.publish(deletion as PublishingEvent,'ghost');
  assert.equal((await f.store.publish({...event,event_id:randomUUID()},'ghost')).result,'stale');
  assert.equal((await f.db.query('SELECT * FROM {{schema}}.activities')).length,0);
});
test('existing imported object URL is preserved instead of reminted',async()=>{
  const event=published();const oldId='https://federation.example/legacy/posts/42';
  await f.db.query('INSERT INTO {{schema}}.object_id_reservations(object_id,content_id) VALUES($1,$2)',[oldId,event.content_id]);
  await f.db.query("INSERT INTO {{schema}}.content_objects(content_id,object_id,instance_id,source_id,source_epoch,state) VALUES($1,$2,$1,'ghost',1,'unpublished')",[event.content_id,oldId]);
  assert.equal((await f.store.publish(event,'ghost')).object_id,oldId);
});
test('failed transaction rolls back object, event receipt and activity together',async()=>{
  await f.db.query("CREATE FUNCTION {{schema}}.reject_activity_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test rollback'; END $$");
  await f.db.query('CREATE TRIGGER reject_activity BEFORE INSERT ON {{schema}}.activities FOR EACH ROW EXECUTE FUNCTION {{schema}}.reject_activity_test()');
  try {await assert.rejects(f.store.publish(published(),'ghost'),/test rollback/);
    assert.equal((await f.db.query('SELECT * FROM {{schema}}.content_objects')).length,0);assert.equal((await f.db.query('SELECT * FROM {{schema}}.publishing_events')).length,0);
  }finally{await f.db.query('DROP TRIGGER reject_activity ON {{schema}}.activities');await f.db.query('DROP FUNCTION {{schema}}.reject_activity_test()');}
});
test('duplicate Follow yields one Accept and valid Undo removes only that relationship',async()=>{
  const input=follow();await f.store.receiveFollow(input);await f.store.receiveFollow(input);
  assert.equal((await f.db.query('SELECT * FROM {{schema}}.followers')).length,1);assert.equal((await f.db.query('SELECT * FROM {{schema}}.activities')).length,1);
  await f.store.receiveUndoFollow({id:`${remoteActor}/undo/1`,actorId:remoteActor,followId:input.id,followActorId:remoteActor,target:actorId});
  assert.equal((await f.db.query('SELECT * FROM {{schema}}.followers')).length,0);
});
test('forged Undo cannot remove another actor; old Undo cannot remove a newer Follow',async()=>{
  const old=follow();await f.store.receiveFollow(old);const newer=follow();await f.store.receiveFollow(newer);
  await assert.rejects(f.store.receiveUndoFollow({id:'https://evil.example/u/1',actorId:'https://evil.example/a',followId:newer.id,followActorId:remoteActor,target:actorId}),rejection('undo_actor_mismatch'));
  await f.store.receiveUndoFollow({id:`${remoteActor}/undo/old`,actorId:remoteActor,followId:old.id,followActorId:remoteActor,target:actorId});
  assert.equal((await f.db.query('SELECT follow_id FROM {{schema}}.followers'))[0]!.follow_id,newer.id);
});
test('out-of-order Undo suppresses its delayed Follow',async()=>{
  const input=follow();await f.store.receiveUndoFollow({id:`${remoteActor}/undo/first`,actorId:remoteActor,followId:input.id,followActorId:remoteActor,target:actorId});
  await f.store.receiveFollow(input);assert.equal((await f.db.query('SELECT * FROM {{schema}}.followers')).length,0);
});
test('foreign activity IDs cannot poison another origin inbox receipts',async()=>{
  const input=follow('https://other.example/follow/1');
  await assert.rejects(f.store.receiveFollow(input),rejection('activity_origin_mismatch'));
  assert.equal((await f.db.query('SELECT * FROM {{schema}}.inbox_receipts')).length,0);
});
test('imported follower without historical Follow ID can unfollow through a verified Undo',async()=>{
  await f.db.query('INSERT INTO {{schema}}.followers(actor_id,inbox,follow_id) VALUES($1,$2,NULL)',[remoteActor,`${remoteActor}/inbox`]);
  const input=follow();await f.store.receiveUndoFollow({id:`${remoteActor}/undo/imported`,actorId:remoteActor,followId:input.id,followActorId:remoteActor,target:actorId});
  assert.equal((await f.db.query('SELECT * FROM {{schema}}.followers')).length,0);
});
