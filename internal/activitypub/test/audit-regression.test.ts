import {test,before,after,beforeEach} from 'node:test';
import assert from 'node:assert/strict';
import {randomUUID} from 'node:crypto';
import {fixture,published} from './helpers.ts';
import {importExistingIdentity} from '../src/import-identity.ts';
import {database,forPublication} from '../src/db.ts';
import type {PublishingEvent} from '../src/contracts.ts';
let f:Awaited<ReturnType<typeof fixture>>;
before(async()=>{f=await fixture();});after(async()=>{await f.close();});beforeEach(async()=>{await f.reset();});
function withdraw(e:ReturnType<typeof published>):PublishingEvent {
 const {document,...base}=e;return {...base,event_id:randomUUID(),type:'content.withdrawn',revision:'2'};
}
test('a retired object URL cannot be claimed by a different CMS content ID',async()=>{
 const first=published();const original=await f.store.publish(first,'ghost');
 await f.store.publish(withdraw(first),'ghost');
 await f.store.publish({...first,event_id:randomUUID(),object_instance:randomUUID(),revision:'3'},'ghost');
 await assert.rejects(f.store.publish(published({object_instance:first.object_instance}),'ghost'),/object_instance_reused/);
 assert.equal((await f.store.object(original.object_id!))?.payload,null);
 assert.equal((await f.db.query('SELECT * FROM {{schema}}.content_objects')).length,1);
});
test('privileged identity import rejects mismatched installation and scope before any write',async()=>{
 await f.db.query('DELETE FROM {{schema}}.identities');
 const bundle={actor_id:f.config.actorId,key_id:f.config.keyId,public_jwk:f.publicJwk,private_jwk:f.privateJwk};
 const wrong=forPublication(database(f.adapter,f.base.schema,'other-installation'),f.config.publicationId);
 await assert.rejects(importExistingIdentity(wrong,{...f.config,installationId:'other-installation'},bundle),/another installation/);
 assert.equal((await f.db.query('SELECT * FROM {{schema}}.identities')).length,0);
 await assert.rejects(importExistingIdentity(f.db,{...f.config,installationId:'other-installation'},bundle),/configuration.*binding/);
 await assert.rejects(importExistingIdentity(f.db,{...f.config,publicationId:'other-publication'},bundle),/configuration.*binding/);
 assert.equal((await f.db.query('SELECT * FROM {{schema}}.identities')).length,0);
});
