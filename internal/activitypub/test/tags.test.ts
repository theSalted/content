import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {randomUUID,createHash} from 'node:crypto';
import {MemoryKvStore} from '@fedify/fedify';
import {publishingEvent,archiveSnapshot,eventHash} from '../src/contracts.ts';
import {makeApp} from '../src/app.ts';import {makeFederation} from '../src/federation.ts';
import {fixture,published,CaptureQueue} from './helpers.ts';
test('authored-tag cross-runtime fixtures preserve valid values and deterministic hashes',async()=>{
 const cases=JSON.parse(await readFile(new URL('../contracts/fixtures/v2-tags.json',import.meta.url),'utf8'));
 for(const c of cases){const result=publishingEvent.safeParse(c.event);assert.equal(result.success,c.valid,c.name);if(result.success){assert.deepEqual(result.data,c.event,c.name);assert.equal(eventHash(result.data),c.sha256,c.name);}}
 const manifest=JSON.parse(await readFile(new URL('../contracts/fixtures/manifest.json',import.meta.url),'utf8'));
 for(const [file,hash] of Object.entries(manifest.sha256))assert.equal(createHash('sha256').update(await readFile(new URL('../contracts/'+file,import.meta.url))).digest('hex'),hash,file);
});
test('authored public tags survive Article serialization; edits replace them and deletion hides them',async()=>{
 const f=await fixture();try{
  const e=published();e.document.tags=[{name:'Photography',href:'https://federation.example/tag/photo/'},{name:'攝影',href:'https://federation.example/tag/camera/'},{name:'Machine Learning',href:'https://federation.example/tag/ml/'},{name:'C++',href:'https://federation.example/tag/cpp/'}];
  const result=await f.store.publish(e,'ghost'),app=makeApp(f.state,makeFederation(f.state,new MemoryKvStore(),new CaptureQueue()));
  const get=()=>app(new Request(result.object_id!,{headers:{accept:'application/activity+json'}}));
  const body=await (await get()).json();
  assert.deepEqual(body.tag.map((x:{type:string;name:string;href:string})=>({type:x.type,name:x.name,href:x.href})),e.document.tags.map(t=>({type:['Machine Learning','C++'].includes(t.name)?'Link':'Hashtag',name:['Machine Learning','C++'].includes(t.name)?t.name:'#'+t.name,href:t.href})));
  const update={...e,event_id:randomUUID(),type:'content.updated' as const,revision:'2',document:{...e.document,tags:[]}};await f.store.publish(update,'ghost');
  assert.equal((await (await get()).json()).tag,undefined);
  await assert.rejects(f.store.publish({...update,document:{...e.document,tags:e.document.tags}},'ghost'),/event_id_conflict/);
  const {document,...base}=e;await f.store.publish({...base,event_id:randomUUID(),type:'content.deleted',revision:'3'},'ghost');assert.equal((await get()).status,410);
 }finally{await f.close();}
});
test('silent archive carries tags without broadcasts and rejects foreign canonical tag origins',async()=>{
 const f=await fixture();try{
  const e=published();e.document.tags=[{name:'Archive',href:'https://federation.example/tag/archive/'}];
  const archive=archiveSnapshot.parse({...e,schema_version:1,type:'content.archived',delivery_policy:'archive',source_state:'published'});
  const result=await f.store.archive(archive,'ghost');assert.equal((await f.store.object(result.object_id!))?.payload?.tag instanceof Array,true);assert.equal((await f.db.query('SELECT 1 FROM {{schema}}.outbound_intents')).length,0);
  const bad={...e,event_id:randomUUID(),content_id:randomUUID(),object_instance:randomUUID(),document:{...e.document,tags:[{name:'Bad',href:'https://foreign.example/tag/bad/'}]}};
  await assert.rejects(f.store.publish(bad,'ghost'),/noncanonical_tag_url/);
 }finally{await f.close();}
});
