import { test,before,after,beforeEach,mock } from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { MemoryKvStore, signRequest } from '@fedify/fedify';
import { Person,CryptographicKey } from '@fedify/vocab';
import { makeFederation,enqueueIntent } from '../src/federation.ts';
import {FeedbackStore} from '../src/feedback.ts';
import { makeApp } from '../src/app.ts';
import { fixture,published,follow,actorId,remoteActor,CaptureQueue } from './helpers.ts';
import type { DocumentLoader } from '@fedify/vocab-runtime';

let f:Awaited<ReturnType<typeof fixture>>;
let app:ReturnType<typeof makeApp>;
let federation:ReturnType<typeof makeFederation>;
let queue:CaptureQueue;
let remoteKeys:CryptoKeyPair;
before(async()=>{
  f=await fixture();
  remoteKeys=await crypto.subtle.generateKey({name:'RSASSA-PKCS1-v1_5',modulusLength:2048,publicExponent:new Uint8Array([1,0,1]),hash:'SHA-256'},true,['sign','verify']);
});
after(async()=>{mock.restoreAll();await f.close();});
beforeEach(async()=>{
  mock.restoreAll();mock.method(globalThis,'fetch',async()=>{throw new Error('Unplanned external network access in protocol test');});
  await f.reset();f.config.federationEnabled=true;f.config.feedbackEnabled=false;f.config.moderationToken='synthetic-moderator-token-not-for-publishing';
  queue=new CaptureQueue();
  const remote=await new Person({id:new URL(remoteActor),preferredUsername:'alice',inbox:new URL(`${remoteActor}/inbox`),
    publicKey:new CryptographicKey({id:new URL(`${remoteActor}#main-key`),owner:new URL(remoteActor),publicKey:remoteKeys.publicKey})}).toJsonLd();
  const loader:DocumentLoader=async url=>{
    if(url.toString()!==remoteActor && url.toString()!==`${remoteActor}#main-key`)throw new Error(`Unexpected fixture URL ${url}`);
    return {document:remote,documentUrl:url.toString(),contextUrl:null};
  };
  federation=makeFederation(f.state,new MemoryKvStore(),queue,loader);
  app=makeApp(f.state,federation);
});
const get=(path:string,accept='application/activity+json')=>new Request(`https://federation.example${path}`,{headers:{accept}});
const post=(body:unknown,token='isolated-test-publisher-token-never-use-live')=>new Request('https://federation.example/internal/v2/events',
  {method:'POST',headers:{'content-type':'application/json',authorization:`Bearer ${token}`},body:JSON.stringify(body)});

test('WebFinger resolves canonical actor and unknown accounts fail',async()=>{
  const response=await app(get('/.well-known/webfinger?resource=acct:editor@federation.example'));
  assert.equal(response.status,200);
  const result=await response.json();assert.equal(result.subject,'acct:editor@federation.example');
  assert.equal(result.links.find((link:{rel:string})=>link.rel==='self').href,actorId);
  assert.equal((await app(get('/.well-known/webfinger?resource=acct:nobody@federation.example'))).status,404);
});
test('actor exposes stable inbox/outbox/followers and preserved public key without private material',async()=>{
  const response=await app(get('/users/editor'));assert.equal(response.status,200);
  const body=await response.json();assert.equal(body.id,actorId);assert.equal(body.outbox,`${actorId}/outbox`);
  assert.equal(body.inbox,`${actorId}/inbox`);assert.equal(body.endpoints.sharedInbox,'https://federation.example/inbox');
  assert.equal(body.publicKey.id,`${actorId}#main-key`);assert.equal(body.publicKey.owner,actorId);
  assert.doesNotMatch(JSON.stringify(body),/privateKey|private_jwk|"d":/);
});
test('untrusted host cannot mint a preview actor identity',async()=>{
  const response=await app(new Request('http://127.0.0.1:3000/users/editor',{headers:{accept:'application/activity+json','x-forwarded-host':'evil.example'}}));
  assert.equal((await response.json()).id,actorId);
});
test('internal publication requires authentication, strict schema and bounded body',async()=>{
  assert.equal((await app(post(published(),'incorrect'))).status,401);
  assert.equal((await app(post({...published(),schema_version:99}))).status,422);
  assert.equal((await app(post({padding:'x'.repeat(2_100_000)}))).status,413);
  assert.equal((await app(post(published()))).status,202);
});
test('empty following is explicitly public and paginated, not inferred private by Mastodon',async()=>{
  const root=await (await app(get('/users/editor/following'))).json();assert.equal(root.totalItems,0);assert.equal(root.first,actorId+'/following?cursor=0');
  const page=await (await app(get('/users/editor/following?cursor=0'))).json();assert.equal(page.type,'OrderedCollectionPage');assert.deepEqual(page.orderedItems??[],[]);assert.equal(page.next,undefined);
});
test('outbox is persisted and paginated, with no unauthenticated POST side effect',async()=>{
  for(let i=0;i<21;i++)await f.store.publish(published(),'ghost');
  const root=await (await app(get('/users/editor/outbox'))).json();assert.equal(root.totalItems,21);assert.ok(root.first);
  const page=await (await app(new Request(root.first,{headers:{accept:'application/activity+json'}}))).json();
  assert.equal(page.orderedItems.length,20);assert.ok(page.next);
  assert.equal((await app(new Request(`${actorId}/outbox`,{method:'POST',body:'{}'}))).status,405);
  assert.equal(queue.messages.length,0);
});
test('objects remain dereferenceable after slug changes; deletion returns tombstone and hides old activities',async()=>{
  const event=published();const result=await f.store.publish(event,'ghost');
  const first=await (await app(new Request(result.object_id!,{headers:{accept:'application/activity+json'}}))).json();
  assert.equal(first.type,'Article');assert.equal(first.attributedTo,actorId);
  await f.store.publish({...event,event_id:randomUUID(),revision:'2',type:'content.updated',document:{...event.document,url:'https://federation.example/blog/changed'}},'ghost');
  const oldCreate=(await f.db.query<{id:string}>("SELECT id FROM {{schema}}.activities WHERE kind='Create'"))[0]!.id;
  await f.store.publish({schema_version:2,publication_id:'publication-example',event_id:randomUUID(),content_id:event.content_id,object_instance:event.object_instance,delivery_policy:'live',source:event.source,revision:'3',occurred_at:event.occurred_at,type:'content.deleted'},'ghost');
  const deleted=await app(new Request(result.object_id!,{headers:{accept:'application/activity+json'}}));
  assert.equal(deleted.status,410);assert.equal((await deleted.json()).type,'Tombstone');
  assert.equal((await app(new Request(oldCreate,{headers:{accept:'application/activity+json'}}))).status,404);
});
test('imported legacy object paths remain resolvable',async()=>{
  const event=published();const id='https://federation.example/old/posts/42';
  await f.db.query('INSERT INTO {{schema}}.object_id_reservations(object_id,content_id) VALUES($1,$2)',[id,event.content_id]);
  await f.db.query("INSERT INTO {{schema}}.content_objects(content_id,object_id,instance_id,source_id,source_epoch,state) VALUES($1,$2,$1,'ghost',1,'unpublished')",[event.content_id,id]);
  await f.store.publish(event,'ghost');const response=await app(new Request(id,{headers:{accept:'application/activity+json'}}));
  assert.equal(response.status,200);assert.equal((await response.json()).id,id);
});
test('unsigned and tampered inbox requests never enter the processing queue',async()=>{
  const body=JSON.stringify(follow().follow);
  assert.ok([400,401,403].includes((await app(new Request(`${actorId}/inbox`,{method:'POST',headers:{'content-type':'application/activity+json'},body}))).status));
  const signed=await signRequest(new Request(`${actorId}/inbox`,{method:'POST',headers:{'content-type':'application/activity+json'},body}),remoteKeys.privateKey,new URL(`${remoteActor}#main-key`));
  const changed=new Request(signed.url,{method:'POST',headers:signed.headers,body:body.replace('Follow','Like')});
  assert.ok([400,401,403].includes((await app(changed)).status));assert.equal(queue.messages.length,0);
});
test('signed Follow is queued before processing, then produces a durable Accept intent',async()=>{
  const body=JSON.stringify(follow().follow);
  const signed=await signRequest(new Request(`${actorId}/inbox`,{method:'POST',headers:{'content-type':'application/activity+json'},body}),remoteKeys.privateKey,new URL(`${remoteActor}#main-key`));
  const response=await app(signed);assert.equal(response.status,202);
  assert.equal((await f.db.query('SELECT * FROM {{schema}}.followers')).length,0);assert.equal(queue.messages.length,1);
  await federation.startQueue(f.state);
  assert.equal((await f.db.query('SELECT * FROM {{schema}}.followers')).length,1);
  assert.equal((await f.db.query("SELECT * FROM {{schema}}.activities WHERE kind='Accept'")).length,1);
});
test('outbound handoff uses Fedify durable queue and stable activity ID, without network delivery',async()=>{
  await f.store.receiveFollow(follow());const job=await f.store.claimIntent(new Date(Date.now()+1000));assert.ok(job);
  await enqueueIntent(federation,f.state,job);assert.equal(queue.messages.length,1);
  assert.ok(queue.messages[0]?.options?.orderingKey);
  assert.match(JSON.stringify(queue.messages[0]!.value),new RegExp(job.activity_id));
});
test('disabled federation rejects inboxes and worker handoff; readiness does not report success',async()=>{
  f.config.federationEnabled=false;
  assert.equal((await app(new Request(`${actorId}/inbox`,{method:'POST',body:'{}'}))).status,503);
  assert.equal((await app(get('/health/ready'))).status,503);
  await f.store.receiveFollow(follow());const job=await f.store.claimIntent(new Date(Date.now()+1000));assert.ok(job);
  await assert.rejects(enqueueIntent(federation,f.state,job),/disabled/);
});

test('API readiness does not depend on a separately deployed worker',async()=>{
 assert.equal((await f.db.query('SELECT * FROM {{schema}}.worker_heartbeats')).length,0);
 assert.equal((await app(get('/health/ready'))).status,200);
 const status=await app(new Request('https://federation.example/internal/v2/status',{headers:{authorization:`Bearer ${f.config.publisherToken}`}}));
 assert.equal((await status.json()).relay_recent,false);
});

test('signed public replies and reactions enter only private moderation; nonpublic replies are ignored',async()=>{
 f.config.feedbackEnabled=true;const event=published(),target=(await f.store.publish(event,'ghost')).object_id!;
 const base={'@context':'https://www.w3.org/ns/activitystreams',actor:remoteActor};
 const note={id:remoteActor+'/notes/'+randomUUID(),type:'Note',attributedTo:remoteActor,inReplyTo:target,content:'<p>A reply</p>',published:'2026-10-26T00:00:00Z',to:['https://www.w3.org/ns/activitystreams#Public']};
 const activities=[{...base,id:note.id+'/create',type:'Create',object:note},
  {...base,id:remoteActor+'/private/'+randomUUID(),type:'Create',object:{...note,id:note.id+'/private',to:[actorId]}},
  {...base,id:remoteActor+'/like/'+randomUUID(),type:'Like',object:target},
  {...base,id:remoteActor+'/boost/'+randomUUID(),type:'Announce',object:target,to:['https://www.w3.org/ns/activitystreams#Public']}];
 for(const activity of activities){const signed=await signRequest(new Request(actorId+'/inbox',{method:'POST',headers:{'content-type':'application/activity+json'},body:JSON.stringify(activity)}),remoteKeys.privateKey,new URL(remoteActor+'#main-key'));assert.equal((await app(signed)).status,202);}
 await federation.startQueue(f.state);const feedback=new FeedbackStore(f.db);
 assert.deepEqual((await feedback.list()).map(x=>x.kind).sort(),['boost','like','reply']);assert.equal((await feedback.visible(target)).length,0);
 const unauth=await app(new Request('https://federation.example/internal/v2/feedback'));assert.equal(unauth.status,401);
 const response=await app(new Request('https://federation.example/internal/v2/feedback',{headers:{authorization:'Bearer '+f.config.moderationToken}}));assert.equal((await response.json()).items.length,3);
});

test('signed feedback edit, Delete and reaction Undo preserve ownership and moderation',async()=>{
 f.config.feedbackEnabled=true;const target=(await f.store.publish(published(),'ghost')).object_id!;
 const feedback=new FeedbackStore(f.db),base={'@context':'https://www.w3.org/ns/activitystreams',actor:remoteActor};
 const note={id:remoteActor+'/notes/'+randomUUID(),type:'Note',attributedTo:remoteActor,inReplyTo:target,content:'<p>First</p>',published:'2026-10-26T00:00:00Z',to:['https://www.w3.org/ns/activitystreams#Public']};
 const submit=async(activity:Record<string,unknown>)=>{const signed=await signRequest(new Request(actorId+'/inbox',{method:'POST',headers:{'content-type':'application/activity+json'},body:JSON.stringify({...base,...activity})}),remoteKeys.privateKey,new URL(remoteActor+'#main-key'));assert.equal((await app(signed)).status,202);await federation.startQueue(f.state);await queue.drain();};
 await submit({id:note.id+'/create',type:'Create',object:note});await moderateCurrent(feedback,note.id,'approved');
 await submit({id:note.id+'/update',type:'Update',object:{...note,content:'<p>Edited</p>',updated:'2026-10-26T00:01:00Z'}});
 assert.equal((await feedback.visible(target)).length,0);assert.equal((await feedback.list())[0]!.html,'<p>Edited</p>');
 await submit({id:note.id+'/bad-update',type:'Update',object:{...note,attributedTo:'https://remote.example/users/other',content:'<p>Forged</p>',updated:'2026-10-26T00:02:00Z'}});
 assert.equal((await feedback.list())[0]!.html,'<p>Edited</p>');
 for(const type of ['Like','Announce']){
  const reaction={id:remoteActor+'/activity/'+randomUUID(),type,actor:remoteActor,object:target,to:['https://www.w3.org/ns/activitystreams#Public']};
  await submit(reaction);await moderateCurrent(feedback,reaction.id,'approved');
  await submit({id:reaction.id+'/undo',type:'Undo',object:reaction});assert.equal((await feedback.visible(target)).length,0);
 }
 await submit({id:note.id+'/delete',type:'Delete',object:note.id});assert.equal((await feedback.list('deleted')).find(x=>x.id===note.id)!.html,'');
});
test('approved feedback collections paginate, hide blocked/private/deleted items and advertise safe links',async()=>{
 f.config.feedbackEnabled=true;const event=published(),target=(await f.store.publish(event,'ghost')).object_id!,feedback=new FeedbackStore(f.db);
 const getUrl=(url:string)=>app(new Request(url,{headers:{accept:'application/activity+json'}}));
 const article=await (await getUrl(target)).json();assert.equal(article.replies,target+'/replies');assert.equal(article.likes,target+'/likes');assert.equal(article.shares,target+'/shares');
 const ids:string[]=[];
 for(let i=0;i<22;i++){const id=remoteActor+'/notes/'+randomUUID();ids.push(id);await feedback.receive({id,activityId:id+'/create',actor:remoteActor,target,kind:'reply',html:'<p>Comment '+i+'</p>',updatedAt:'2026-10-26T00:00:00Z'});if(i<21)await moderateCurrent(feedback,id,'approved');}
 const root=await getUrl(target+'/replies'),body=await root.json();assert.equal(body.totalItems,21);assert.equal(root.headers.get('cache-control'),'no-store');
 const page=await (await getUrl(body.first)).json();assert.equal(page.orderedItems.length,20);assert.ok(page.orderedItems.every((x:{attributedTo:string})=>x.attributedTo===remoteActor));
 const last=await (await getUrl(page.next)).json();assert.equal(last.orderedItems.length,1);assert.equal(last.next,undefined);
 assert.equal((await getUrl(target+'/replies?cursor=bad')).status,400);
 await feedback.block(remoteActor,true);assert.equal((await (await getUrl(target+'/replies')).json()).totalItems,0);
 const {document,...base}=event;await f.store.publish({...base,event_id:randomUUID(),revision:'2',type:'content.deleted'},'ghost');
 assert.equal((await getUrl(target+'/replies')).status,404);assert.equal((await getUrl(target)).status,410);
});

async function moderateCurrent(store:FeedbackStore,id:string,decision:'approved'|'rejected'){
 const [row]=await store.db.query<{activity_id:string}>('SELECT activity_id FROM {{schema}}.feedback WHERE id=$1',[id]);
 return store.moderate(id,decision,row?.activity_id??'https://invalid.example/missing');
}

test('owner moderation requires exact reviewed activity revision and paginates private results',async()=>{
 f.config.feedbackEnabled=true;const feedback=new FeedbackStore(f.db),target=(await f.store.publish(published(),'ghost')).object_id!;
 const id=remoteActor+'/notes/'+randomUUID(),input={id,activityId:id+'/create',actor:remoteActor,target,kind:'reply' as const,html:'<p>Reviewed</p>',updatedAt:'2026-10-26T00:00:00Z'};await feedback.receive(input);
 const base='https://federation.example/internal/v2/feedback';
 const owner=(path:string,body?:unknown)=>app(new Request(base+path,{method:body?'POST':'GET',headers:{authorization:'Bearer '+f.config.moderationToken,'content-type':'application/json'},...(body?{body:JSON.stringify(body)}:{})}));
 const list=await owner('?limit=1');assert.equal(list.headers.get('cache-control'),'no-store');const before=await list.json();assert.equal(before.items[0].activity_id,input.activityId);assert.equal(before.next_cursor,null);
 await feedback.receive({...input,activityId:id+'/update',updatedAt:'2026-10-26T00:01:00Z',html:'<p>Unreviewed edit</p>'});
 assert.equal((await owner('/moderate',{id,decision:'approved'})).status,422);
 const stale=await owner('/moderate',{id,decision:'approved',expected_activity_id:input.activityId});assert.equal(stale.status,409);assert.equal((await stale.json()).error,'feedback_changed');assert.equal((await feedback.visible(target)).length,0);
 assert.equal((await owner('/moderate',{id,decision:'approved',expected_activity_id:id+'/update'})).status,200);assert.equal((await feedback.visible(target)).length,1);
 assert.equal((await owner('?limit=101')).status,400);assert.equal((await owner('?cursor=bad')).status,400);
 for(let i=0;i<2;i++)await feedback.receive({...input,id:id+'/'+i,activityId:id+'/'+i+'/create'});
 const page=await (await owner('?limit=1')).json();assert.equal(page.items.length,1);assert.equal(page.next_cursor,'1');assert.equal((await (await owner('?limit=1&cursor='+page.next_cursor)).json()).next_cursor,null);
});
