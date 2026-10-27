import {test} from 'node:test';import assert from 'node:assert/strict';import {randomUUID} from 'node:crypto';import {MemoryKvStore} from '@fedify/fedify';
import {fixture,published,CaptureQueue} from './helpers.ts';import {resolveReceipt} from '../src/receipts.ts';import {eventHash,archiveSnapshot} from '../src/contracts.ts';import {makeApp} from '../src/app.ts';import {makeFederation} from '../src/federation.ts';import {splitIngress} from '../src/ingress.ts';
test('receipt binds exact applied event, same-instance edits, withdrawal and republish without writes',async()=>{
 const f=await fixture();try{const e=published(),ack=await f.store.publish(e,'ghost');const resolve=()=>resolveReceipt(f.store,e,'ghost');const p=await resolve();assert.equal(p.projection?.object_id,ack.object_id);assert.equal(p.event_hash,eventHash(e));
 await f.store.publish({...e,event_id:randomUUID(),type:'content.updated',revision:'2',document:{...e.document,title:'Changed'}},'ghost');assert.equal((await resolve()).projection?.revision,'2');
 const stale={...e,event_id:randomUUID()};await f.store.publish(stale,'ghost');assert.equal((await resolveReceipt(f.store,stale,'ghost')).reason,'stale_receipt');
 const {document,...base}=e;await f.store.publish({...base,event_id:randomUUID(),revision:'3',type:'content.withdrawn'},'ghost');assert.equal((await resolve()).reason,'not_public');
 await f.store.publish({...e,event_id:randomUUID(),object_instance:randomUUID(),revision:'4'},'ghost');assert.equal((await resolve()).reason,'superseded');assert.equal((await resolve()).projection,null);
 await assert.rejects(resolveReceipt(f.store,{...e,revision:'9'},'ghost'),/event_id_conflict/);await assert.rejects(resolveReceipt(f.store,{...e,event_id:randomUUID()},'ghost'),/receipt_not_found/);
 await assert.rejects(resolveReceipt(f.store,{...e,publication_id:'other'},'ghost'),/publication_mismatch/);await assert.rejects(resolveReceipt(f.store,e,'other'),/publisher_source_mismatch/);
 const counts=async()=>f.db.query('SELECT (SELECT count(*) FROM {{schema}}.publishing_events) AS events,(SELECT count(*) FROM {{schema}}.activities) AS activities,(SELECT count(*) FROM {{schema}}.outbound_intents) AS intents');const before=await counts();await resolve();assert.deepEqual(await counts(),before);
 }finally{await f.close()}
});
test('private receipt resolver accepts silent archive while intake disabled; dedicated moderation cannot publish or resolve',async()=>{
 const f=await fixture();try{const archive=archiveSnapshot.parse({...published(),schema_version:1,type:'content.archived',delivery_policy:'archive',source_state:'published'});await f.store.archive(archive,'ghost');
 f.config.moderationToken='synthetic-moderation-token-separate';f.config.feedbackEnabled=true;f.config.archiveEnabled=false;
 const app=makeApp(f.state,makeFederation(f.state,new MemoryKvStore(),new CaptureQueue()));const route='/internal/v2/receipts/resolve';
 const request=(path=route,token=f.config.publisherToken,body:unknown={schema_version:1,event:archive},method='POST')=>new Request('https://federation.example'+path,{method,headers:{authorization:'Bearer '+token,'content-type':'application/json'},...(method==='POST'?{body:JSON.stringify(body)}:{})});
 const ok=await app(request());assert.equal(ok.status,200);assert.equal((await ok.json()).projection.state,'public');assert.equal((await splitIngress(app,true).publicFetch(request())).status,404);
 assert.equal((await app(request(route,f.config.moderationToken))).status,401);assert.equal((await app(request('/internal/v2/events',f.config.moderationToken,published()))).status,401);
 assert.equal((await app(request('/internal/v2/status',f.config.moderationToken,undefined,'GET'))).status,401);
 assert.equal((await app(request('/internal/v2/feedback',f.config.moderationToken,undefined,'GET'))).status,200);assert.equal((await app(request('/internal/v2/feedback',f.config.publisherToken,undefined,'GET'))).status,401);
 f.config.moderationToken=undefined;assert.equal((await app(request('/internal/v2/feedback',f.config.publisherToken,undefined,'GET'))).status,401);
 assert.equal((await app(request(route,f.config.publisherToken,{schema_version:1,event:archive,extra:true}))).status,422);
 assert.deepEqual(await f.store.archive(archive,'ghost'),{result:'duplicate',original_result:'applied'});
 }finally{await f.close()}
});

test('receipt cross-runtime fixture pins validate exact canonical event hashes',async()=>{
 const {readFile}=await import('node:fs/promises');const {createHash}=await import('node:crypto');const {publishingEvent}=await import('../src/contracts.ts');
 const raw=await readFile(new URL('../contracts/receipt-v1/fixtures.json',import.meta.url),'utf8'),manifest=JSON.parse(await readFile(new URL('../contracts/receipt-v1/manifest.json',import.meta.url),'utf8'));
 assert.equal(createHash('sha256').update(raw).digest('hex'),manifest.files['fixtures.json']);const parsed=JSON.parse(raw);assert.equal(parsed.fixtures.length,7);
 for(const f of parsed.fixtures){const event=(f.query.event.type==='content.archived'?archiveSnapshot:publishingEvent).parse(f.query.event);assert.equal(eventHash(event),f.response.event_hash);assert.equal(f.response.event_id,event.event_id);assert.equal(f.response.publication_id,event.publication_id);assert.deepEqual(f.response.source,event.source);}
});
