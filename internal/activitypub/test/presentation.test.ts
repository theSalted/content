import {test} from 'node:test';import assert from 'node:assert/strict';
import {MemoryKvStore} from '@fedify/fedify';
import {articlePresentation} from '../src/article-presentation.ts';
import {fixture,published,CaptureQueue} from './helpers.ts';
import {makeFederation} from '../src/federation.ts';import {makeApp} from '../src/app.ts';
import {chooseBanner} from '../src/profile.ts';
import {readConfig} from '../src/config.ts';
import {randomUUID} from 'node:crypto';
test('article media keeps author alt text, resolves relative images and excludes active URL schemes',()=>{
 const r=articlePresentation('<p>Article</p><img src="/photo.jpg" alt="A tree &amp; sky" onerror="alert(1)"><img src="https://cdn.example/p.webp" alt="Second"><img src="javascript:alert(1)"><img src="data:text/html,bad"><img src="/photo.jpg">','https://example.org/blog/post');
 assert.deepEqual(r.attachments,[{type:'Image',url:'https://example.org/photo.jpg',name:'A tree & sky',mediaType:'image/jpeg'},{type:'Image',url:'https://cdn.example/p.webp',name:'Second',mediaType:'image/webp'}]);
 assert.equal(r.html,'<p>Article</p>');assert.equal(articlePresentation('<img src="https://user:password@cdn.example/img.png">','https://example.org/').attachments.length,0);
});
test('actor profile emits configured bio/icon/header and website, with unchanged identity',async()=>{
 const f=await fixture();try{
  Object.assign(f.config,{summary:'<p>Existing biography</p>',iconUrl:'https://assets.example/avatar.png',imageUrl:'https://assets.example/header.jpg',iconAlt:'Portrait',imageAlt:'Landscape'});
  const fed=makeFederation(f.state,new MemoryKvStore(),new CaptureQueue()),app=makeApp(f.state,fed);
  const response=await app(new Request(f.config.actorId,{headers:{accept:'application/activity+json'}}));const actor=await response.json();
  assert.equal(actor.id,f.config.actorId);assert.equal(actor.summary,f.config.summary);assert.equal(actor.icon.url,f.config.iconUrl);assert.equal(actor.image.url,f.config.imageUrl);assert.equal(actor.icon.name,'Portrait');
  assert.ok(JSON.stringify(actor.attachment).includes('Website'));const field=Array.isArray(actor.attachment)?actor.attachment[0]:actor.attachment;assert.ok(field.value.includes('rel="me"'));
 }finally{await f.close();}
});
test('image attachments survive AP serialization and browser fallback respects deletion',async()=>{
 const f=await fixture();try{
  const event=published();event.document.html='<p>Image article</p><img src="/image.png" alt="A detailed caption">';const result=await f.store.publish(event,'ghost');
  const app=makeApp(f.state,makeFederation(f.state,new MemoryKvStore(),new CaptureQueue()));
  const ap=await (await app(new Request(result.object_id!,{headers:{accept:'application/activity+json'}}))).json();
  const image=Array.isArray(ap.attachment)?ap.attachment[0]:ap.attachment;assert.equal(image.type,'Image');assert.equal(image.name,'A detailed caption');assert.equal(image.url,'https://federation.example/image.png');
  const human=await app(new Request(result.object_id!,{headers:{accept:'text/html'}}));assert.equal(human.status,302);assert.equal(human.headers.get('location'),event.document.url);
  const {document,...base}=event;await f.store.publish({...base,event_id:randomUUID(),type:'content.deleted',revision:'2'},'ghost');
  assert.equal((await app(new Request(result.object_id!,{headers:{accept:'text/html'}}))).status,410);
 }finally{await f.close();}
});

test('banner list selects per fetch without changing identity, validators or query strings',async()=>{
 const f=await fixture();try{
  const urls=['https://assets.example/one.jpg','https://assets.example/two.png'];f.config.imageUrls=urls;
  assert.equal(chooseBanner(f.config,()=>0),urls[0]);assert.equal(chooseBanner(f.config,()=>1),urls[1]);
  const app=makeApp(f.state,makeFederation(f.state,new MemoryKvStore(),new CaptureQueue()));
  for(let i=0;i<4;i++){
   const res=await app(new Request(f.config.actorId,{headers:{accept:'application/activity+json','if-none-match':'"stale"','if-modified-since':'Mon, 26 Oct 2026 00:00:00 GMT'}}));
   assert.equal(res.status,200);assert.equal(res.headers.get('cache-control'),'no-store');assert.equal(res.headers.get('etag'),null);assert.equal(res.headers.get('last-modified'),null);
   const actor=await res.json();assert.equal(actor.id,f.config.actorId);assert.equal(actor.publicKey.id,f.config.keyId);assert.ok(urls.includes(actor.image.url));
  }
 }finally{await f.close();}
});
test('banner configuration rejects unsafe or unbounded lists and has a stable single-image fallback',()=>{
 const env={ACTIVITYPUB_SCHEMA:'example_ap',CONTENT_INSTALLATION_ID:'example',PUBLICATION_ID:'example',ACTOR_ID:'https://example.org/actors/editor',ACTOR_KEY_ID:'https://example.org/actors/editor#main-key',ACTOR_NAME:'Editor',ACTOR_PROFILE_URL:'https://example.org/about',DATABASE_URL:'postgres://test',EXPECTED_PUBLIC_KEY_SHA256:'a'.repeat(64),PUBLISHER_ID:'cms',PUBLISHER_TOKEN:'x'.repeat(40)};
 for(const value of ['{}','[]','no json',JSON.stringify(Array(33).fill('https://assets.example/a')),JSON.stringify(['http://assets.example/a']),JSON.stringify(['https://user:pass@assets.example/a'])])assert.throws(()=>readConfig({...env,ACTOR_IMAGE_URLS:value}));
 const config=readConfig({...env,ACTOR_IMAGE_URL:'https://assets.example/fallback.jpg',ACTOR_IMAGE_URLS:'["https://assets.example/one.jpg","https://assets.example/one.jpg"]'});
 assert.deepEqual(config.imageUrls,['https://assets.example/one.jpg']);assert.equal(chooseBanner(config),'https://assets.example/one.jpg');assert.equal(chooseBanner({imageUrl:config.imageUrl}),config.imageUrl);
});
