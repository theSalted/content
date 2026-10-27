import { test } from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { fileURLToPath } from 'node:url';
import { connect,forPublication,validateRuntimeRole } from '../src/db.ts';
import { migrateSchema } from '../src/migrate.ts';
import {FeedbackStore,validateFeedbackStorage} from '../src/feedback.ts';
import { Store } from '../src/store.ts';
import {archiveSnapshot} from '../src/contracts.ts';
import { DurableQueue } from '../src/queue.ts';
import { DatabaseKvStore } from '../src/kv.ts';
import { loadIdentity,fingerprint } from '../src/identity.ts';
import { importExistingIdentity } from '../src/import-identity.ts';
import { makeFederation,enqueueIntent } from '../src/federation.ts';
import { makeApp } from '../src/app.ts';
import { runtime } from '../src/runtime.ts';
import { Person,CryptographicKey } from '@fedify/vocab';
import { signRequest } from '@fedify/fedify';
import { actorId,remoteActor,follow,published } from './helpers.ts';
import type { Config } from '../src/config.ts';

const url=process.env.FEDERATION_TEST_DATABASE_URL;
test('real PostgreSQL driver, concurrency, crash recovery and signed inbox handoff',{skip:!url},async t=>{
  const target=new URL(url!);
  assert.ok(['127.0.0.1','localhost','[::1]'].includes(target.hostname),'Test database must be local');
  assert.ok(target.pathname.endsWith('_test'),'Test database name must end in _test');
  const schema=`fixture_${randomUUID().replaceAll('-','')}`;
  const admin=connect(url!,schema);const owner=schema+'_owner';const runtimeRole=schema+'_runtime';
  await admin.db.query(`CREATE ROLE ${owner} LOGIN NOSUPERUSER NOBYPASSRLS`);
  await admin.db.query(`CREATE ROLE ${runtimeRole} LOGIN NOSUPERUSER NOBYPASSRLS`);
  await admin.db.query(`CREATE SCHEMA ${schema} AUTHORIZATION ${owner}`);
  const ownerUrl=new URL(url!);ownerUrl.username=owner;
  const c=connect(ownerUrl.href,schema);const peer=connect(ownerUrl.href,schema);
  t.after(async()=>{await c.close();await peer.close();await admin.db.query(`DROP SCHEMA ${schema} CASCADE`);await admin.db.query(`DROP ROLE ${runtimeRole}`);await admin.db.query(`DROP ROLE ${owner}`);await admin.close();});
  await migrateSchema(c.db);
  await c.db.query('INSERT INTO {{schema}}.publication_roles VALUES($1,$2),($1,$3)',['publication-example',owner,runtimeRole]);
  const base=c.db;c.db=forPublication(c.db,'publication-example');peer.db=forPublication(peer.db,'publication-example');
  const store=new Store(c.db,actorId);
  await t.test('concurrent silent archive creates one historical object and no recipient jobs',async()=>{
    const live=published();const archived=archiveSnapshot.parse({...live,schema_version:1,type:'content.archived',delivery_policy:'archive',source_state:'published',document:{...live.document,published_at:'2020-01-02T03:04:05Z'}});
    const other=new Store(peer.db,actorId);
    const receipts=await Promise.all([store.archive(archived,'ghost'),other.archive(archived,'ghost')]);
    assert.deepEqual(receipts.map(x=>x.result).sort(),['applied','duplicate']);
    assert.equal((await c.db.query('SELECT 1 FROM {{schema}}.activities WHERE content_id=$1',[archived.content_id])).length,1);
    assert.equal((await c.db.query('SELECT 1 FROM {{schema}}.outbound_intents i JOIN {{schema}}.activities a ON a.id=i.activity_id WHERE a.content_id=$1',[archived.content_id])).length,0);
    await assert.rejects(other.archive({...archived,event_id:randomUUID()},'ghost'),/archive_content_exists/);
    const updated={...live,event_id:randomUUID(),revision:'2',type:'content.updated' as const};await store.publish(updated,'ghost');
    const object=await store.object(receipts[0]!.object_id!);assert.equal(object?.payload?.published,archived.document.published_at);
    await assert.rejects(other.archive({...archived,event_id:randomUUID(),revision:'3'},'ghost'),/archive_content_exists/);
  });
  let runtimeFixture:Config;
  const q=new DurableQueue(c.db);const q2=new DurableQueue(peer.db);
  await t.test('migrations and PostgreSQL KV survive reconnect without regenerating identity',async()=>{
    await migrateSchema(c.db);
    const kv=new DatabaseKvStore(c.db);
    await kv.set(['synthetic','value'],{retained:true});
    const other=new DatabaseKvStore(peer.db);
    assert.deepEqual(await other.get(['synthetic','value']),{retained:true});
    await assert.rejects(loadIdentity(c.db,{actorId,keyId:`${actorId}#main-key`,expectedFingerprint:'0'.repeat(64)}),/missing/);
  });
  await t.test('concurrent duplicate publication commits exactly one activity',async()=>{
    const event=published();const results=await Promise.all([store.publish(event,'ghost'),new Store(peer.db,actorId).publish(event,'ghost')]);
    assert.deepEqual(results.map(r=>r.result).sort(),['applied','duplicate']);
    assert.equal((await c.db.query('SELECT * FROM {{schema}}.activities WHERE content_id=$1',[event.content_id])).length,1);
    const conflict=published();const outcome=await Promise.allSettled([store.publish(conflict,'ghost'),new Store(peer.db,actorId).publish({...conflict,content_id:randomUUID()},'ghost')]);
    assert.equal(outcome.filter(r=>r.status==='fulfilled').length,1);
    assert.match((outcome.find(r=>r.status==='rejected') as PromiseRejectedResult).reason.message,/event_id_conflict/);
  });
  await t.test('concurrent initial and republish claims cannot reuse current or retired object URLs',async()=>{
    const instance=randomUUID();const a=published({object_instance:instance});
    const b=published({object_instance:instance});
    const first=await Promise.allSettled([store.publish(a,'ghost'),new Store(peer.db,actorId).publish(b,'ghost')]);
    assert.equal(first.filter(r=>r.status==='fulfilled').length,1);
    assert.match((first.find(r=>r.status==='rejected') as PromiseRejectedResult).reason.message,/object_instance_reused/);
    const winner=first[0]!.status==='fulfilled'?a:b;
    const original=(first.find(r=>r.status==='fulfilled') as PromiseFulfilledResult<{object_id:string}>).value.object_id;
    const {document,...baseEvent}=winner;
    await store.publish({...baseEvent,event_id:randomUUID(),revision:'2',type:'content.withdrawn'},'ghost');
    const next={...winner,event_id:randomUUID(),revision:'3',object_instance:randomUUID()};
    const concurrent=await Promise.allSettled([store.publish(next,'ghost'),new Store(peer.db,actorId).publish(published({object_instance:instance}),'ghost')]);
    assert.equal(concurrent[0]!.status,'fulfilled');assert.equal(concurrent[1]!.status,'rejected');
    assert.match((concurrent[1] as PromiseRejectedResult).reason.message,/object_instance_reused/);
    await assert.rejects(store.publish(published({object_instance:instance}),'ghost'),/object_instance_reused/);
    assert.equal((await store.object(original))?.payload,null);
    // An initial publish and a republish competing for the same fresh URL also
    // have exactly one winner. No transient reservation escapes a failed tx.
    const {document:nextDocument,...nextBase}=next;
    await store.publish({...nextBase,event_id:randomUUID(),revision:'4',type:'content.withdrawn'},'ghost');
    const contested=randomUUID();
    const claims=await Promise.allSettled([store.publish({...next,event_id:randomUUID(),revision:'5',object_instance:contested},'ghost'),new Store(peer.db,actorId).publish(published({object_instance:contested}),'ghost')]);
    assert.equal(claims.filter(r=>r.status==='fulfilled').length,1);
    assert.match((claims.find(r=>r.status==='rejected') as PromiseRejectedResult).reason.message,/object_instance_reused/);
    assert.equal((await store.object(original))?.payload,null);
  });
  await t.test('workers cannot handle the same ordered key concurrently',async()=>{
    await q.enqueue({id:'first'},{orderingKey:'article'});await q.enqueue({id:'next'},{orderingKey:'article'});
    let release!:()=>void;const gate=new Promise<void>(r=>{release=r;});let started!:()=>void;const start=new Promise<void>(r=>{started=r;});
    const first=q.processOne(async()=>{started();await gate;});await start;
    assert.equal(await q2.processOne(()=>assert.fail('same key overtook')),false);
    release();await first;assert.equal(await q2.processOne(message=>assert.deepEqual(message,{id:'next'})),true);
  });
  await t.test('killed worker process preserves its unacknowledged job',{timeout:10000},async sub=>{
    await q.enqueue({id:'crash'});
    const child=spawn(process.execPath,[fileURLToPath(new URL('./crash-worker.fixture.ts',import.meta.url)),schema],
      {env:{FEDERATION_TEST_DATABASE_URL:ownerUrl.href},stdio:['ignore','ignore','inherit','ipc']});
    sub.after(()=>{if(child.exitCode===null&&child.signalCode===null)child.kill('SIGKILL');});
    const exit=once(child,'exit');await once(child,'message');
    // Kill only the child created above, after it reports holding its job transaction.
    child.kill('SIGKILL');await exit;
    assert.equal(await q2.processOne(message=>assert.deepEqual(message,{id:'crash'})),true);
    assert.equal((await q2.getDepth()).queued,0);
  });
  await t.test('signed Follow persists before processing; Accept handoff is durable',async()=>{
    const keys=await crypto.subtle.generateKey({name:'RSASSA-PKCS1-v1_5',modulusLength:2048,publicExponent:new Uint8Array([1,0,1]),hash:'SHA-256'},true,['sign','verify']);
    const config:Config={databaseUrl:ownerUrl.href,schema,installationId:'test-installation',publicationId:'publication-example',actorRoute:'/users/{identifier}',displayName:'Editor',profileUrl:'https://example.org/about',poolSize:5,actorId,keyId:`${actorId}#main-key`,origin:new URL(actorId).origin,identifier:'editor',expectedFingerprint:await fingerprint(keys.publicKey),publisherId:'ghost',publisherToken:'synthetic-test-only-never-use-in-production',federationEnabled:true,host:'127.0.0.1',port:3000};
    runtimeFixture=config;
    const bundle={actor_id:actorId,key_id:config.keyId,public_jwk:await crypto.subtle.exportKey('jwk',keys.publicKey),private_jwk:await crypto.subtle.exportKey('jwk',keys.privateKey)};
    const wrongImport=connect(ownerUrl.href,schema,'wrong-installation');
    try{
      await assert.rejects(importExistingIdentity(forPublication(wrongImport.db,config.publicationId),{...config,installationId:'wrong-installation'},bundle),/another installation/);
      assert.equal((await c.db.query('SELECT * FROM {{schema}}.identities')).length,0);
    }finally{await wrongImport.close();}
    await importExistingIdentity(c.db,config,bundle);
    const identity=await loadIdentity(c.db,config);const state={store,identity,config};
    const remote=await new Person({id:new URL(remoteActor),inbox:new URL(`${remoteActor}/inbox`),publicKey:new CryptographicKey({id:new URL(`${remoteActor}#main-key`),owner:new URL(remoteActor),publicKey:keys.publicKey})}).toJsonLd();
    const kv=new DatabaseKvStore(c.db);
    const federation=makeFederation(state,kv,{
      nativeRetrial:true,enqueue:q.enqueue.bind(q),listen:async handler=>{await q.processOne(handler);}
    },async address=>{assert.ok([remoteActor,`${remoteActor}#main-key`].includes(address.toString()));return {document:remote,documentUrl:address.toString(),contextUrl:null};});
    const app=makeApp(state,federation);const input=follow();
    const req=await signRequest(new Request(`${actorId}/inbox`,{method:'POST',headers:{'content-type':'application/activity+json'},body:JSON.stringify(input.follow)}),keys.privateKey,new URL(`${remoteActor}#main-key`));
    assert.equal((await app(req)).status,202);assert.equal((await q2.getDepth()).queued,1);
    assert.equal((await c.db.query('SELECT * FROM {{schema}}.followers')).length,0);
    await federation.startQueue(state);assert.equal((await c.db.query('SELECT * FROM {{schema}}.followers')).length,1);
    const intent=await store.claimIntent(new Date(Date.now()+1000));assert.ok(intent);
    await store.handoffIntent(intent,job=>enqueueIntent(federation,state,job));
    assert.equal((await q2.getDepth()).queued,1);
    assert.equal((await c.db.query('SELECT state FROM {{schema}}.outbound_intents WHERE id=$1',[intent.id]))[0]!.state,'enqueued');
    // Deliberately do not deliver to any external inbox. Queue payloads are synthetic.
  });
  await t.test('runtime grants deny DDL, identity writes, other schemas and unauthorized publication scope',async()=>{
    await admin.db.query(`GRANT USAGE ON SCHEMA ${schema} TO ${runtimeRole}`);
    await admin.db.query(`GRANT SELECT ON ${schema}.installation,${schema}.publication_roles,${schema}.identities TO ${runtimeRole}`);
    await admin.db.query(`GRANT SELECT,INSERT,UPDATE,DELETE ON ${schema}.followers TO ${runtimeRole}`);
    const runtimeUrl=new URL(url!);runtimeUrl.username=runtimeRole;
    const r=connect(runtimeUrl.href,schema);try{
      await validateRuntimeRole(r.db);
      await assert.rejects(validateRuntimeRole(base),/non-owner/);
      await assert.rejects(r.db.query('CREATE TABLE {{schema}}.forbidden(id int)'),/permission denied/);
      await assert.rejects(forPublication(r.db,'publication-example').query('DELETE FROM {{schema}}.identities'),/permission denied/);
      assert.equal((await forPublication(r.db,'publication-example').query('SELECT * FROM {{schema}}.followers')).length,1);
      assert.equal((await forPublication(r.db,'unrelated').query('SELECT * FROM {{schema}}.followers')).length,0);
      await assert.rejects(forPublication(r.db,'unrelated').query('INSERT INTO {{schema}}.followers(actor_id,inbox) VALUES($1,$2)',['https://else.example/a','https://else.example/i']),/row-level security/);
      await assert.rejects(r.db.query(`SELECT * FROM pg_authid`),/permission denied/);
      await admin.db.query(`CREATE SCHEMA ${schema}_other`);
      await admin.db.query(`CREATE TABLE ${schema}_other.private_data(id int)`);
      try{await assert.rejects(r.db.query(`SELECT * FROM ${schema}_other.private_data`),/permission denied/);}finally{await admin.db.query(`DROP SCHEMA ${schema}_other CASCADE`);}
    }finally{await r.close();}
  });
  await t.test('configured runtime boots and accepts an event using only explicit restricted grants',async()=>{
    const tables=['content_objects','publishing_events','activities','inbox_receipts','followers','undone_follows','outbound_intents','delivery_failures','worker_heartbeats','transport_jobs','federation_kv','retired_objects'];
    await admin.db.query(`GRANT SELECT,INSERT,UPDATE,DELETE ON ${tables.map(name=>schema+'.'+name).join(',')} TO ${runtimeRole}`);
    await admin.db.query(`GRANT SELECT,INSERT ON ${schema}.object_id_reservations TO ${runtimeRole}`);
    await admin.db.query(`GRANT SELECT,INSERT,UPDATE ON ${schema}.feedback TO ${runtimeRole}`);
    await admin.db.query(`GRANT SELECT,INSERT ON ${schema}.feedback_tombstones TO ${runtimeRole}`);
    await admin.db.query(`GRANT SELECT,INSERT,DELETE ON ${schema}.feedback_blocks TO ${runtimeRole}`);
    await admin.db.query(`GRANT USAGE ON ALL SEQUENCES IN SCHEMA ${schema} TO ${runtimeRole}`);
    const runtimeUrl=new URL(url!);runtimeUrl.username=runtimeRole;
    const app=await runtime({...runtimeFixture!,databaseUrl:runtimeUrl.href});
    try{
      const input=published();const handler=makeApp(app.state,app.federation);
      const response=await handler(new Request(new URL('/internal/v2/events',actorId),{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer '+runtimeFixture!.publisherToken},body:JSON.stringify(input)}));
      assert.equal(response.status,202);assert.equal((await response.json()).result,'applied');
      const actor=await handler(new Request(actorId,{headers:{accept:'application/activity+json'}}));
      assert.equal(actor.status,200);assert.equal((await actor.json()).id,actorId);
      const kv=new DatabaseKvStore(app.state.store.db);await kv.set(['runtime'],'restricted-role');
      assert.equal(await kv.get(['runtime']),'restricted-role');
      const queue=new DurableQueue(app.state.store.db);const before=await queue.getDepth();
      await queue.enqueue({id:'restricted-runtime-synthetic'});assert.equal((await queue.getDepth()).queued,before.queued+1);
      await assert.rejects(app.state.store.db.query('DELETE FROM {{schema}}.identities'),/permission denied/);
    }finally{await app.close();}
  });
  await t.test('feedback migration and restricted runtime enforce publication isolation',async()=>{
    const runtimeUrl=new URL(url!);runtimeUrl.username=runtimeRole;const r=connect(runtimeUrl.href,schema);
    try{
      const db=forPublication(r.db,'publication-example'),f=new FeedbackStore(db);
      await validateFeedbackStorage(db);
      await admin.db.query(`REVOKE UPDATE ON ${schema}.feedback FROM ${runtimeRole}`);
      try{await assert.rejects(validateFeedbackStorage(db),/exact runtime privileges/);}finally{await admin.db.query(`GRANT UPDATE ON ${schema}.feedback TO ${runtimeRole}`);}
      await validateFeedbackStorage(db);
      const e=published(),target=(await new Store(db,actorId).publish(e,'ghost')).object_id!;
      const input={id:remoteActor+'/notes/'+randomUUID(),activityId:remoteActor+'/create/'+randomUUID(),actor:remoteActor,target,kind:'reply' as const,html:'<p>Synthetic reply</p>',updatedAt:new Date().toISOString()};
      await Promise.all([f.receive(input),new FeedbackStore(peer.db).receive(input)]);
      assert.equal((await f.list()).length,1);await moderateCurrent(f,input.id,'approved');assert.equal((await f.visible(target)).length,1);
      const other=new FeedbackStore(forPublication(r.db,'unrelated'));
      assert.equal((await other.list()).length,0);
      await assert.rejects(forPublication(r.db,'unrelated').query('INSERT INTO {{schema}}.feedback_blocks(actor_id) VALUES($1)',[remoteActor]),/row-level security/);
      const clash={...input,id:remoteActor+'/notes/'+randomUUID(),activityId:remoteActor+'/create/'+randomUUID()};
      const claims=await Promise.allSettled([f.receive(clash),new FeedbackStore(peer.db).receive({...clash,actor:'https://remote.example/users/other',activityId:'https://remote.example/create/'+randomUUID()})]);
      assert.equal(claims.filter(x=>x.status==='fulfilled').length,1);assert.equal(claims.filter(x=>x.status==='rejected').length,1);
      await f.block(remoteActor,true);assert.equal((await f.visible(target)).length,0);
      await assert.rejects(moderateCurrent(f,input.id,'approved'),/feedback_not_found/);
      await f.remove(remoteActor,input.id);assert.equal((await f.list('deleted'))[0]!.html,'');
      await assert.rejects(db.query('DELETE FROM {{schema}}.feedback'),/permission denied/);
      await assert.rejects(db.query('UPDATE {{schema}}.feedback_tombstones SET actor_id=actor_id'),/permission denied/);
      await assert.rejects(db.query('ALTER TABLE {{schema}}.feedback DISABLE ROW LEVEL SECURITY'),/owner|permission/);
      const flags=await admin.db.query<{relrowsecurity:boolean;relforcerowsecurity:boolean}>("SELECT relrowsecurity,relforcerowsecurity FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname IN ('feedback','feedback_tombstones','feedback_blocks')",[schema]);
      assert.equal(flags.length,3);assert.ok(flags.every(x=>x.relrowsecurity&&x.relforcerowsecurity));
    }finally{await r.close();}
  });
  await t.test('runtime rejects inherited migration ownership and SET ROLE elevation',async()=>{
    const runtimeUrl=new URL(url!);runtimeUrl.username=runtimeRole;const r=connect(runtimeUrl.href,schema);
    try{
      for(const options of ['INHERIT TRUE, SET FALSE','INHERIT FALSE, SET TRUE','INHERIT FALSE, SET FALSE, ADMIN TRUE']){
        await admin.db.query(`GRANT ${owner} TO ${runtimeRole} WITH ${options}`);
        try{await assert.rejects(validateRuntimeRole(r.db),/non-owner|unsafe|protected|privilege|elevation/);}finally{await admin.db.query(`REVOKE ${owner} FROM ${runtimeRole}`);}
      }
      await validateRuntimeRole(r.db);
    }finally{await r.close();}
  });
  await t.test('runtime rejects protected-table and column write privileges',async()=>{
    const runtimeUrl=new URL(url!);runtimeUrl.username=runtimeRole;const r=connect(runtimeUrl.href,schema);
    try{
      for(const privilege of ['UPDATE (private_jwk)','INSERT','TRUNCATE']){
        await admin.db.query(`GRANT ${privilege} ON ${schema}.identities TO ${runtimeRole}`);
        try{await assert.rejects(validateRuntimeRole(r.db),/protected|privilege|unsafe/);}finally{await admin.db.query(`REVOKE ${privilege} ON ${schema}.identities FROM ${runtimeRole}`);}
      }
      await admin.db.query(`GRANT UPDATE ON ${schema}.object_id_reservations TO ${runtimeRole}`);
      try{await assert.rejects(validateRuntimeRole(r.db),/protected|privilege/);}finally{await admin.db.query(`REVOKE UPDATE ON ${schema}.object_id_reservations FROM ${runtimeRole}`);}
      await validateRuntimeRole(r.db);
    }finally{await r.close();}
  });
  await t.test('same namespace migrations serialize and another installation cannot claim schema',async()=>{
    await Promise.all([migrateSchema(base),migrateSchema(peer.db)]);
    const foreign=connect(ownerUrl.href,schema,'unrelated-installation');
    try{await assert.rejects(migrateSchema(foreign.db),/another installation/);}finally{await foreign.close();}
  });

  await t.test('two publications with equal content/event IDs and remote actors remain isolated on PostgreSQL',async()=>{
    const other='another-publication';
    await base.query('INSERT INTO {{schema}}.publication_roles VALUES($1,$2)',[other,owner]);
    const otherDB=forPublication(base,other),otherActor='https://different.example/actors/author';
    const otherStore=new Store(otherDB,otherActor),input=follow();
    await otherStore.receiveFollow({...input,target:otherActor,follow:{...input.follow,object:otherActor}});
    const e=published();await store.publish(e,'ghost');
    const result=await otherStore.publish({...e,publication_id:other,document:{...e.document,url:'https://different.example/article'}},'ghost');
    assert.equal(await store.object(result.object_id!),undefined);
    assert.equal((await otherStore.followers(0)).length,1);
    await otherDB.query('DELETE FROM {{schema}}.followers');assert.equal((await store.followers(0)).length,1);
    const k1=new DatabaseKvStore(c.db),k2=new DatabaseKvStore(otherDB);
    await k1.set(['shared'],{publication:'first'});await k2.set(['shared'],{publication:'second'});
    assert.deepEqual(await k1.get(['shared']),{publication:'first'});
  });
  await t.test('reservation upgrade backfills all publications and rejects pre-existing URL ambiguity atomically',async()=>{
    const upgradeSchema=schema+'_upgrade';await admin.db.query(`CREATE SCHEMA ${upgradeSchema} AUTHORIZATION ${owner}`);
    const upgrade=connect(ownerUrl.href,upgradeSchema);
    try{
      await migrateSchema(upgrade.db);
      await upgrade.db.query('INSERT INTO {{schema}}.publication_roles VALUES($1,current_user),($2,current_user)',['first','second']);
      for(const publication of ['first','second']){
        const db=forPublication(upgrade.db,publication),s=new Store(db,actorId);
        const event=published({publication_id:publication});await s.publish(event,'ghost');
        const {document,...baseEvent}=event;await s.publish({...baseEvent,event_id:randomUUID(),revision:'2',type:'content.withdrawn'},'ghost');
        await s.publish({...event,event_id:randomUUID(),revision:'3',object_instance:randomUUID()},'ghost');
      }
      // Synthetic recreation of the previously released 001-only layout.
      await upgrade.db.query('DROP TABLE {{schema}}.object_id_reservations CASCADE');
      await upgrade.db.query("DELETE FROM {{schema}}.schema_migrations WHERE version='002'");
      await migrateSchema(upgrade.db);
      for(const publication of ['first','second'])assert.equal((await forPublication(upgrade.db,publication).query('SELECT * FROM {{schema}}.object_id_reservations')).length,2);
      await upgrade.db.query('DROP TABLE {{schema}}.object_id_reservations CASCADE');
      await upgrade.db.query("DELETE FROM {{schema}}.schema_migrations WHERE version='002'");
      const scoped=forPublication(upgrade.db,'first');
      await scoped.query('INSERT INTO {{schema}}.retired_objects(object_id,published_at,changed_at) SELECT object_id,published_at,changed_at FROM {{schema}}.content_objects');
      await assert.rejects(migrateSchema(upgrade.db),/duplicate key/);
      assert.equal((await upgrade.db.query<{name:string|null}>('SELECT to_regclass($1)::text AS name',[upgradeSchema+'.object_id_reservations']))[0]!.name,null);
      assert.equal((await upgrade.db.query("SELECT version FROM {{schema}}.schema_migrations WHERE version='002'")).length,0);
      const policies=await upgrade.db.query<{relrowsecurity:boolean;relforcerowsecurity:boolean}>("SELECT relrowsecurity,relforcerowsecurity FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname IN ('content_objects','retired_objects')",[upgradeSchema]);
      assert.ok(policies.every(row=>row.relrowsecurity&&row.relforcerowsecurity));
    }finally{await upgrade.close();await admin.db.query(`DROP SCHEMA ${upgradeSchema} CASCADE`);}
  });
  await t.test('first migration refuses a nonempty foreign module schema without changing its tables',async()=>{
    const foreignSchema=schema+'_foreign';await admin.db.query(`CREATE SCHEMA ${foreignSchema} AUTHORIZATION ${owner}`);
    const foreign=connect(ownerUrl.href,foreignSchema);
    try{
      await foreign.db.query('CREATE TABLE {{schema}}.installation(singleton boolean PRIMARY KEY,installation_id text NOT NULL)');
      await foreign.db.query("INSERT INTO {{schema}}.installation VALUES(true,'test-installation')");
      await assert.rejects(migrateSchema(foreign.db),/another module namespace/);
      assert.equal((await foreign.db.query('SELECT tablename FROM pg_tables WHERE schemaname=$1',[foreignSchema])).length,1);
    }finally{await foreign.close();await admin.db.query(`DROP SCHEMA ${foreignSchema} CASCADE`);}
  });
  await t.test('distinct owned schemas migrate independently in the same PostgreSQL database',async()=>{
    const secondSchema=schema+'_second';await admin.db.query(`CREATE SCHEMA ${secondSchema} AUTHORIZATION ${owner}`);
    const second=connect(ownerUrl.href,secondSchema,'another-installation');
    try{await Promise.all([migrateSchema(base),migrateSchema(second.db)]);
      assert.equal((await second.db.query<{installation_id:string}>('SELECT installation_id FROM {{schema}}.installation'))[0]!.installation_id,'another-installation');
      assert.equal((await base.query<{installation_id:string}>('SELECT installation_id FROM {{schema}}.installation'))[0]!.installation_id,'test-installation');
    }finally{await second.close();await admin.db.query(`DROP SCHEMA ${secondSchema} CASCADE`);}
  });

});

async function moderateCurrent(store:FeedbackStore,id:string,decision:'approved'|'rejected'){
 const [row]=await store.db.query<{activity_id:string}>('SELECT activity_id FROM {{schema}}.feedback WHERE id=$1',[id]);
 return store.moderate(id,decision,row?.activity_id??'https://invalid.example/missing');
}
