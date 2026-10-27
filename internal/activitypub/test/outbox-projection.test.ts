import {test} from 'node:test';
import assert from 'node:assert/strict';
import {randomUUID} from 'node:crypto';
import {fixture,published} from './helpers.ts';

test('profile outbox counts current articles once, with latest text, stable Create ID and no tombstones',async()=>{
 const f=await fixture();try{
 const post=published();await f.store.publish(post,'ghost');
 const original=(await f.store.outbox(0))[0]!.payload;
 await f.store.publish({...post,event_id:randomUUID(),revision:'2',type:'content.updated',document:{...post.document,title:'Edited'}},'ghost');
 const edited=await f.store.outbox(0);assert.equal(edited.length,1);assert.equal(edited[0]!.payload.id,original.id);assert.equal((edited[0]!.payload.object as Record<string,unknown>).name,'Edited');
 assert.equal(await f.store.outboxCount(),1);
 const {document,...base}=post;
 await f.store.publish({...base,event_id:randomUUID(),revision:'3',type:'content.withdrawn'},'ghost');
 assert.equal(await f.store.outboxCount(),0);assert.deepEqual(await f.store.outbox(0),[]);
 const replacement={...post,event_id:randomUUID(),revision:'4',object_instance:randomUUID()};await f.store.publish(replacement,'ghost');
 const republished=await f.store.outbox(0);assert.equal(republished.length,1);assert.notEqual(republished[0]!.payload.id,original.id);assert.equal((republished[0]!.payload.object as Record<string,unknown>).id, f.config.origin+'/ap/objects/'+post.publication_id+'/'+replacement.object_instance);
 assert.equal(await f.store.outboxCount(),1);
 assert.equal((await f.db.query("SELECT count(*)::int AS n FROM {{schema}}.activities"))[0]!.n,4);
 const stored=(await f.db.query<{payload:{object:{name:string}}}>("SELECT payload FROM {{schema}}.activities WHERE id=$1",[original.id]))[0]!;assert.equal(stored.payload.object.name,post.document.title);
 }finally{await f.close()}
});

test('profile pages and counter use the same current-public projection',async()=>{
 const f=await fixture();try{
 for(let n=0;n<23;n++){const post=published({document:{...published().document,title:String(n)}});await f.store.publish(post,'ghost');await f.store.publish({...post,event_id:randomUUID(),revision:'2',type:'content.updated'},'ghost');}
 assert.equal(await f.store.outboxCount(),23);
 const first=await f.store.outbox(0,20),second=await f.store.outbox(20,20);assert.equal(first.length,20);assert.equal(second.length,3);
 assert.equal(new Set([...first,...second].map(a=>a.payload.id)).size,23);
 }finally{await f.close()}
});
