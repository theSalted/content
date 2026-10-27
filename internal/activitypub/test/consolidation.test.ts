import {test,before,after,beforeEach} from 'node:test';
import assert from 'node:assert/strict';
import {randomUUID,createHash} from 'node:crypto';
import {readFile} from 'node:fs/promises';
import {fixture,published,follow,publicationId,remoteActor} from './helpers.ts';
import {forPublication,database} from '../src/db.ts';
import {Store} from '../src/store.ts';
import {DurableQueue} from '../src/queue.ts';
import {DatabaseKvStore} from '../src/kv.ts';
import {createModule} from '../src/module.ts';
import {publishingEvent,eventHash} from '../src/contracts.ts';
import {schemaIdentifier,migrationLockId} from '../src/namespace.ts';
import {migrateSchema} from '../src/migrate.ts';
let f:Awaited<ReturnType<typeof fixture>>;
before(async()=>{f=await fixture();});
after(async()=>{await f.close();});
beforeEach(async()=>{await f.reset();});
test('disabled module needs no database, publisher or identity configuration',async()=>{
 const app=await createModule({});assert.equal((await app.fetch(new Request('https://example.org/health/live'))).status,200);
 assert.equal((await app.fetch(new Request('https://example.org/inbox',{method:'POST'}))).status,503);await app.close();
});
test('two unrelated publications isolate objects, followers, inbox receipts, queues and KV',async()=>{
 const other='other-publication',otherActor='https://unrelated.example/actors/writer';
 await f.base.query('INSERT INTO {{schema}}.publication_roles VALUES($1,current_user) ON CONFLICT DO NOTHING',[other]);
 const db=forPublication(f.base,other),store=new Store(db,otherActor);
 const input=follow();await f.store.receiveFollow(input);await store.receiveFollow({...input,target:otherActor,follow:{...input.follow,object:otherActor}});
 const first=published(),second={...first,publication_id:other,document:{...first.document,url:'https://unrelated.example/article'}};
 await f.store.publish(first,'ghost');await store.publish(second,'ghost');
 assert.equal((await f.store.followers(0)).length,1);assert.equal((await store.followers(0)).length,1);
 assert.equal((await f.store.outbox(0)).length,1);assert.equal((await store.outbox(0)).length,1);
 const otherId=(await db.query<{object_id:string}>('SELECT object_id FROM {{schema}}.content_objects'))[0]!.object_id;
 assert.equal(await f.store.object(otherId),undefined);await assert.rejects(f.store.publish(second,'ghost'),/publication_mismatch/);
 await db.query('DELETE FROM {{schema}}.followers');assert.equal((await f.store.followers(0)).length,1);
 const a=new DurableQueue(f.db),b=new DurableQueue(db);
 await a.enqueue({id:'same'},{orderingKey:'same'});await b.enqueue({id:'same'},{orderingKey:'same'});
 assert.equal((await a.getDepth()).queued,1);await b.processOne(()=>{});assert.equal((await a.getDepth()).queued,1);
 const ak=new DatabaseKvStore(f.db),bk=new DatabaseKvStore(db);
 await ak.set(['same'],'first');await bk.set(['same'],'second');assert.equal(await ak.get(['same']),'first');
 assert.equal((await forPublication(f.base,'not-granted').query('SELECT * FROM {{schema}}.identities')).length,0);
 await assert.rejects(forPublication(f.base,'not-granted').query('INSERT INTO {{schema}}.followers(actor_id,inbox) VALUES($1,$2)',[remoteActor,remoteActor+'/inbox']),/row-level security/);
});
test('republish retains CMS identity, replaces only object instance and preserves old tombstone',async()=>{
 const initial=published(),old=await f.store.publish(initial,'ghost');
 const base={schema_version:2 as const,publication_id:publicationId,event_id:randomUUID(),content_id:initial.content_id,
 object_instance:initial.object_instance,delivery_policy:'live' as const,source:initial.source,revision:'2',occurred_at:initial.occurred_at};
 await f.store.publish({...base,type:'content.withdrawn'},'ghost');
 const fresh={...initial,event_id:randomUUID(),object_instance:randomUUID(),revision:'3'};
 const current=await f.store.publish(fresh,'ghost');assert.notEqual(current.object_id,old.object_id);
 assert.equal((await f.store.object(old.object_id!))?.payload,null);
 assert.equal((await f.store.object(current.object_id!))?.content_id,initial.content_id);
 assert.deepEqual((await f.store.outbox(0)).map(r=>r.payload.type),['Create']);
 await assert.rejects(f.store.publish({...initial,event_id:randomUUID(),revision:'4',type:'content.updated'},'ghost'),/object_instance_mismatch/);
 await f.store.publish({...base,event_id:randomUUID(),object_instance:fresh.object_instance,revision:'5',type:'content.deleted'},'ghost');
 await assert.rejects(f.store.publish({...fresh,event_id:randomUUID(),object_instance:randomUUID(),revision:'6'},'ghost'),/tombstone_is_final/);
});
test('64-bit revisions preserve ordering beyond JavaScript safe integer range',async()=>{
 const e=published({revision:'9007199254740992'});await f.store.publish(e,'ghost');
 await f.store.publish({...e,event_id:randomUUID(),revision:'9007199254740993',type:'content.updated'},'ghost');
 assert.equal((await f.store.publish({...e,event_id:randomUUID()},'ghost')).result,'stale');
 const [row]=await f.db.query<{revision:string}>('SELECT revision::text FROM {{schema}}.content_objects');assert.equal(row?.revision,'9007199254740993');
});
test('schema identifiers reject public, system and injection names; migration binds installation',async()=>{
 for(const s of ['public','pg_catalog','information_schema','a.b','a";DROP TABLE x;--'])assert.throws(()=>schemaIdentifier(s));
 assert.notEqual(migrationLockId('a','example_ap'),migrationLockId('b','example_ap'));
 assert.notEqual(migrationLockId('a','example_ap'),migrationLockId('a','other_ap'));
 await assert.rejects(migrateSchema(database(f.adapter,f.base.schema,'other-installation')),/another installation/);
});
test('shared cross-runtime fixture corpus accepts/rejects and hashes deterministically',async()=>{
 const cases=JSON.parse(await readFile(new URL('../contracts/fixtures/v2-cases.json',import.meta.url),'utf8'));
 for(const c of cases){const r=publishingEvent.safeParse(c.event);assert.equal(r.success,c.valid,c.name);if(r.success)assert.equal(eventHash(r.data),c.sha256,c.name);}
});

test('frozen cross-runtime lifecycle covers lost acknowledgments, republish and irreversible delete',async()=>{
 const lifecycle=JSON.parse(await readFile(new URL('../contracts/fixtures/v2-lifecycle.json',import.meta.url),'utf8'));
 for(const step of lifecycle.steps){
  const e=publishingEvent.parse(step.event);
  if(step.error)await assert.rejects(f.store.publish(e,'content'),new RegExp(step.error),step.name);
  else {const result=await f.store.publish(e,'content');assert.equal(result.result,step.result,step.name);
   if(step.original_result)assert.equal(result.original_result,step.original_result,step.name);}
 }
 const rows=await f.db.query<{state:string;revision:string}>('SELECT state,revision::text FROM {{schema}}.content_objects');
 assert.equal(rows[0]!.state,lifecycle.final_state);assert.equal(rows[0]!.revision,lifecycle.final_revision);
 const activities=await f.db.query<{kind:string}>('SELECT kind FROM {{schema}}.activities ORDER BY sequence');
 assert.deepEqual(activities.map(r=>r.kind),lifecycle.expected_activity_kinds);
 const manifest=JSON.parse(await readFile(new URL('../contracts/fixtures/manifest.json',import.meta.url),'utf8'));
 for(const [path,checksum] of Object.entries(manifest.sha256)){
  assert.equal(createHash('sha256').update(await readFile(new URL('../contracts/'+path,import.meta.url))).digest('hex'),checksum,path);
 }
});
