import {test,before,after,beforeEach} from 'node:test';
import assert from 'node:assert/strict';
import {randomUUID} from 'node:crypto';
import {MemoryKvStore} from '@fedify/fedify';
import {fixture,published,follow,CaptureQueue} from './helpers.ts';
import {archiveSnapshot,publishingEvent,eventHash,type ArchiveSnapshot} from '../src/contracts.ts';
import {makeFederation} from '../src/federation.ts';
import {makeApp} from '../src/app.ts';
import {splitIngress} from '../src/ingress.ts';
let f:Awaited<ReturnType<typeof fixture>>;
before(async()=>{f=await fixture();});after(async()=>{await f.close();});beforeEach(async()=>{await f.reset();f.config.archiveEnabled=false;});
function snapshot(overrides:Partial<ArchiveSnapshot>={}):ArchiveSnapshot{
 const event=published();return {...event,schema_version:1,type:'content.archived',delivery_policy:'archive',source_state:'published',occurred_at:'2020-03-04T12:00:00Z',document:{...event.document,published_at:'2020-02-03T12:00:00Z'},...overrides};
}
const request=(body:unknown,path='/internal/v2/archive',token='isolated-test-publisher-token-never-use-live')=>new Request('https://federation.example'+path,{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer '+token},body:JSON.stringify(body)});
test('archive is strict, public/published only and distinct from live events',()=>{
 const s=snapshot();assert.ok(archiveSnapshot.safeParse(s).success);assert.equal(publishingEvent.safeParse(s).success,false);
 for(const state of ['draft','scheduled','private'])assert.equal(archiveSnapshot.safeParse({...s,source_state:state}).success,false);
 for(const visibility of ['private','members','paid'])assert.equal(archiveSnapshot.safeParse({...s,document:{...s.document,visibility}}).success,false);
 assert.equal(archiveSnapshot.safeParse({...s,delivery_policy:'live'}).success,false);
 assert.notEqual(eventHash(s),eventHash({...published(),event_id:s.event_id}));
});
test('historical enrollment retains dates, permalink and identity while producing zero delivery jobs',async()=>{
 await f.store.receiveFollow(follow());await f.db.query('TRUNCATE {{schema}}.outbound_intents');
 const s=snapshot(),r=await f.store.archive(s,'ghost');assert.equal(r.result,'applied');
 const row=await f.store.object(r.object_id!);assert.equal(row?.payload?.url,s.document.url);assert.equal(row?.payload?.published,s.document.published_at);assert.equal(row?.payload?.updated,s.occurred_at);
 const outbox=await f.store.outbox(0);assert.equal(outbox.length,1);assert.equal(outbox[0]!.payload.published,s.document.published_at);
 assert.equal((await f.db.query('SELECT * FROM {{schema}}.outbound_intents')).length,0);assert.equal((await f.db.query('SELECT * FROM {{schema}}.transport_jobs')).length,0);
 assert.equal((await f.store.archive(s,'ghost')).result,'duplicate');assert.equal((await f.store.outbox(0)).length,1);
 await assert.rejects(f.store.archive({...s,document:{...s.document,title:'different'}},'ghost'),/event_id_conflict/);
 await assert.rejects(f.store.archive({...s,event_id:randomUUID(),revision:'2'},'ghost'),/archive_content_exists/);
});
test('archive order follows original publication dates, not import order',async()=>{
 const newer=snapshot({document:{...snapshot().document,title:'Newer',published_at:'2022-01-01T00:00:00Z'}});
 const older=snapshot({document:{...snapshot().document,title:'Older',published_at:'2019-01-01T00:00:00Z'}});
 await f.store.archive(newer,'ghost');await f.store.archive(older,'ghost');
 assert.deepEqual((await f.store.outbox(0)).map(r=>(r.payload.object as {name:string}).name),['Newer','Older']);
});
test('later live edits deliver Update with original object/date, and deletion hides archived HTML',async()=>{
 const s=snapshot(),r=await f.store.archive(s,'ghost');await f.store.receiveFollow(follow());await f.db.query('TRUNCATE {{schema}}.outbound_intents');
 const {source_state,...base}=s;
 await f.store.publish({...base,schema_version:2,type:'content.updated',delivery_policy:'live',event_id:randomUUID(),revision:'2',occurred_at:'2026-10-26T00:00:00Z',document:{...s.document,title:'Edited archive'}},'ghost');
 const row=await f.store.object(r.object_id!);assert.equal(row?.payload?.published,s.document.published_at);assert.equal(row?.payload?.name,'Edited archive');
 assert.equal((await f.db.query('SELECT * FROM {{schema}}.outbound_intents')).length,1);
 const {document,...gone}=base;await f.store.publish({...gone,schema_version:2,type:'content.deleted',delivery_policy:'live',event_id:randomUUID(),revision:'3'},'ghost');
 assert.equal((await f.store.object(r.object_id!))?.payload,null);
 assert.equal((await f.store.outbox(0)).some(x=>['Create','Update'].includes(String(x.payload.type))),false);
 await assert.rejects(f.store.archive({...s,event_id:randomUUID(),revision:'4'},'ghost'),/archive_content_exists/);
 // An exact lost-ack retry after a later delete is harmless and cannot resurrect it.
 assert.equal((await f.store.archive(s,'ghost')).result,'duplicate');assert.equal((await f.store.object(r.object_id!))?.payload,null);
});
test('archive obeys publication/source/canonical URL and global object reservations',async()=>{
 const s=snapshot();await assert.rejects(f.store.archive({...s,publication_id:'another'},'ghost'),/publication_mismatch/);
 await assert.rejects(f.store.archive(s,'different'),/publisher_source_mismatch/);
 await assert.rejects(f.store.archive({...s,document:{...s.document,url:'https://cms.example/internal'}},'ghost'),/noncanonical_article_url/);
 await f.store.archive(s,'ghost');await assert.rejects(f.store.archive({...snapshot(),object_instance:s.object_instance},'ghost'),/object_instance_reused/);
});
test('archive is authenticated, default disabled, private-only and not accepted as a live event',async()=>{
 const queue=new CaptureQueue(),fed=makeFederation(f.state,new MemoryKvStore(),queue),app=makeApp(f.state,fed),split=splitIngress(app,true),s=snapshot();
 assert.equal((await split.publicFetch(request(s))).status,404);assert.equal((await app(request(s,undefined,'wrong'))).status,401);
 assert.equal((await app(request(s))).status,403);f.config.archiveEnabled=true;
 assert.equal((await app(request(s,'/internal/v2/events'))).status,422);assert.equal((await app(request(s))).status,202);
 assert.equal((await app(request({...s,source_state:'draft'}))).status,422);assert.equal(queue.messages.length,0);
 const out=await (await app(new Request(f.config.actorId+'/outbox?cursor=0',{headers:{accept:'application/activity+json'}}))).json();
 const article=out.orderedItems[0].object;assert.equal(article.url,s.document.url);assert.equal(article.published,s.document.published_at);
 assert.equal((await app(new Request(article.id,{headers:{accept:'application/activity+json'}}))).status,200);
});
