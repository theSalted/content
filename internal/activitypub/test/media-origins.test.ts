import {test} from 'node:test';import assert from 'node:assert/strict';import {randomUUID} from 'node:crypto';
import {MemoryKvStore} from '@fedify/fedify';
import {readPublicMediaMapping,publicMediaUrl,publicMediaHtml,projectArticleMedia} from '../src/media-origins.ts';
import {Store,PUBLIC} from '../src/store.ts';import {eventHash} from '../src/contracts.ts';
import {fixture,published,CaptureQueue} from './helpers.ts';import {makeApp} from '../src/app.ts';import {makeFederation} from '../src/federation.ts';
const old='https://legacy.example.org',target='https://images.example.org',path='/content/images/0123456789abcdef01234567/image.png',url=old+path;
const mapping=readPublicMediaMapping(target,old)!;
const image=(value:any)=>Array.isArray(value.attachment)?value.attachment[0]:value.attachment;
test('explicit media mapping rejects partial, ambiguous or nonpublic origin configuration',()=>{
 assert.equal(readPublicMediaMapping(),undefined);
 for(const [to,from] of [[target,undefined],[undefined,old],[target,''],[target,old+'/'],[target,old+'/path'],[target,'https://user:pass@legacy.example.org'],[target,old+'?x'],[target,'https://127.0.0.1'],[target,'https://x.internal'],['http://images.example.org',old],[target,Array.from({length:9},(_,i)=>`https://s${i}.example.org`).join(',')]])assert.throws(()=>readPublicMediaMapping(to,from));
 assert.deepEqual(readPublicMediaMapping(target,old+','+old),mapping);
});
test('aliases are exact owned media paths only, never arbitrary rewrites',()=>{
 assert.equal(publicMediaUrl(url,mapping),target+path);
 for(const bad of [url+'?x',url+'#x',url.replace('https:','http:'),'//legacy.example.org'+path,url.replace('legacy.example.org','legacy.example.org.attacker.test'),url.replace('legacy.example.org','user@legacy.example.org'),old+'/ghost/images/0123456789abcdef01234567/image.png',url.replace('image.png','image.svg'),url.replace('/image.png','/%69mage.png'),url.replace('/content/','/foo/../content/'),url.toUpperCase(),url+' ',target+path,'data:image/png,abc'])assert.equal(publicMediaUrl(bad,mapping),bad);
});
test('HTML edits only image attribute source spans; text, links, scripts and all unrelated bytes survive',()=>{
 const source=`<!-- ${url} --><P data-x='stay'><a href="${url}">${url}</a><img SRC = "${url}" alt='&quot; > &amp;' srcset='${url} 1x, https://external.example/image.jpg 2x'><script>"<img src='${url}'>"</script><img src=${url}><img src="${url}?private=1"></P>`;
 const expected=source.replace(`SRC = "${url}"`,`SRC = "${target+path}"`).replace(`srcset='${url}`,`srcset='${target+path}`).replace(`src=${url}>`,`src=${target+path}>`);
 assert.equal(publicMediaHtml(source,mapping),expected);
});
test('public Article projection preserves identity, language, alt text, and non-image fields without mutating source',()=>{
 const article={type:'Article',id:'https://example.org/object/1',url,attributedTo:'https://example.org/actor',published:'2020-01-01T00:00:00Z',updated:'2021-01-01T00:00:00Z',to:[PUBLIC],cc:['https://example.org/followers'],content:`<img src='${url}'>`,contentMap:{en:`<img src="${url}">`},attachment:[{type:'Image',url,name:'Original alt'},{type:'Image',url:{type:'Link',href:url}},{type:'Document',url}],tag:[{type:'Link',href:url}]};
 const before=structuredClone(article),projected=projectArticleMedia(article,mapping) as typeof article;
 assert.deepEqual(article,before);assert.equal(projected.attachment[0]!.url,target+path);assert.deepEqual(projected.attachment[1]!.url,{type:'Link',href:target+path});assert.equal(projected.attachment[2]!.url,url);
 for(const name of ['id','url','attributedTo','published','updated','to','cc','tag'] as const)assert.deepEqual(projected[name],article[name]);
 assert.equal(projected.attachment[0]!.name,'Original alt');assert.ok(projected.contentMap.en.includes(target));
 const privateArticle={...article,to:['https://example.org/private'],cc:[]};assert.equal(projectArticleMedia(privateArticle,mapping),privateArticle);
 const tombstone={type:'Tombstone',id:article.id};assert.equal(projectArticleMedia(tombstone,mapping),tombstone);assert.equal(projectArticleMedia(article),article);
});
test('current object, imported fallback and outbox normalize without changing stored snapshots, hashes, dates or intents; withdrawal stays private',async()=>{
 const f=await fixture();try{
  const event=published();event.document.html=`<p>Original</p><img src="${url}" alt="Alt">`;
  const receipt=await f.store.publish(event,'ghost');
  const capture=async()=>({objects:await f.db.query('SELECT to_jsonb(o) AS value FROM {{schema}}.content_objects o ORDER BY content_id'),activities:await f.db.query('SELECT to_jsonb(a) AS value FROM {{schema}}.activities a ORDER BY id'),events:await f.db.query('SELECT to_jsonb(e) AS value FROM {{schema}}.publishing_events e ORDER BY event_id'),intents:await f.db.query('SELECT to_jsonb(i) AS value FROM {{schema}}.outbound_intents i ORDER BY id')});
  const before=await capture();f.config.publicMedia=mapping;
  const queue=new CaptureQueue(),app=makeApp(f.state,makeFederation(f.state,new MemoryKvStore(),queue));
  const get=async(id:string)=>{const r=await app(new Request(id,{headers:{accept:'application/activity+json','if-none-match':'"stale"','if-modified-since':'Wed, 31 Dec 2030 00:00:00 GMT'}}));assert.equal(r.status,200);assert.equal(r.headers.get('cache-control'),'no-store');assert.equal(r.headers.get('etag'),null);assert.equal(r.headers.get('last-modified'),null);return r.json();};
  const object=await get(receipt.object_id!);assert.equal(object.id,receipt.object_id);assert.equal(image(object).url,target+path);assert.equal(image(object).name,'Alt');assert.equal(object.published,event.document.published_at);
  const page=await get(f.config.actorId+'/outbox?cursor=0');assert.equal(image(page.orderedItems[0].object).url,target+path);assert.equal(page.orderedItems[0].object.id,receipt.object_id);
  const rawActivity=(await f.store.outbox(0))[0]!.payload;const historic=await (await app(new Request(String(rawActivity.id),{headers:{accept:'application/activity+json'}}))).json();assert.equal(image(historic.object).url,url);
  assert.deepEqual(await capture(),before);assert.equal(queue.messages.length,0);
  // Imported IDs use the exact fallback path, with the same representation guard.
  const imported=f.config.origin+'/legacy/article';await f.db.query('INSERT INTO {{schema}}.object_id_reservations(object_id,content_id) VALUES($1,$2)',[imported,event.content_id]);await f.db.query('UPDATE {{schema}}.content_objects SET object_id=$2,payload=jsonb_set(payload,\'{id}\',to_jsonb($2::text)) WHERE content_id=$1',[event.content_id,imported]);
  assert.equal(image(await get(imported)).url,target+path);
  const {document,...base}=event;await f.store.publish({...base,event_id:randomUUID(),type:'content.withdrawn',revision:'2'},'ghost');
  const gone=await app(new Request(imported,{headers:{accept:'application/activity+json'}}));assert.equal(gone.status,410);assert.equal((await gone.json()).type,'Tombstone');assert.equal(await f.store.outboxCount(),0);
  const unpublished=published();const {document:unused,...unpublishedBase}=unpublished;await f.store.publish({...unpublishedBase,type:'content.withdrawn'},'ghost');
  assert.equal((await app(new Request(f.config.origin+'/ap/objects/'+unpublished.publication_id+'/'+unpublished.object_instance,{headers:{accept:'application/activity+json'}}))).status,404);
 }finally{await f.close();}
});
test('future normal Create and Update normalize before freezing while exact original event receipt hashes and duplicate handling survive',async()=>{
 const f=await fixture();try{
  const store=new Store(f.db,f.config.actorId,undefined,undefined,mapping),event=published();event.document.html=`<p>Text</p><img src="${url}" alt="Keep">`;
  const source=structuredClone(event);const result=await store.publish(event,'ghost');assert.deepEqual(event,source);
  const assertSnapshot=async()=>{const object=await store.object(result.object_id!);assert.equal((object!.payload!.attachment as {url:string}[])[0]!.url,target+path);const activities=await f.db.query<{payload:{object:{attachment:{url:string}[]}}}>('SELECT payload FROM {{schema}}.activities');for(const a of activities)assert.equal(a.payload.object.attachment[0]!.url,target+path);};
  await assertSnapshot();assert.equal((await f.db.query('SELECT payload_hash FROM {{schema}}.publishing_events WHERE event_id=$1',[event.event_id]))[0]!.payload_hash,eventHash(event));
  assert.equal((await store.publish(event,'ghost')).result,'duplicate');
  await store.publish({...event,type:'content.updated',event_id:randomUUID(),revision:'2',document:{...event.document,title:'Edited'}},'ghost');await assertSnapshot();
  assert.equal((await f.db.query('SELECT count(*)::int AS n FROM {{schema}}.activities'))[0]!.n,2);
 }finally{await f.close();}
});
test('every paginated current-public outbox object uses the media view, with unchanged totals and stored originals',async()=>{
 const f=await fixture();try{
  for(let i=0;i<22;i++){const event=published();event.document.html=`<p>Archive ${i}</p><img src="${url}">`;await f.store.publish(event,'ghost');}
  f.config.publicMedia=mapping;const app=makeApp(f.state,makeFederation(f.state,new MemoryKvStore(),new CaptureQueue()));
  const get=async(id:string)=>{const r=await app(new Request(id,{headers:{accept:'application/activity+json'}}));assert.equal(r.status,200);return r.json();};
  const collection=await get(f.config.actorId+'/outbox');assert.equal(collection.totalItems,22);
  let next=typeof collection.first==='string'?collection.first:collection.first.id,count=0;const ids=new Set();
  while(next){const page=await get(next);for(const item of page.orderedItems){assert.equal(image(item.object).url,target+path);ids.add(item.object.id);count++;}next=typeof page.next==='string'?page.next:page.next?.id;}
  assert.equal(count,22);assert.equal(ids.size,22);
  assert.equal((await f.db.query('SELECT count(*)::int AS n FROM {{schema}}.content_objects WHERE strpos(payload::text,$1)>0',[old]))[0]!.n,22);
  assert.equal((await f.db.query('SELECT count(*)::int AS n FROM {{schema}}.outbound_intents'))[0]!.n,0);
 }finally{await f.close();}
});
