import {PGlite} from '@electric-sql/pglite';
import {database,forPublication,type Adapter,type Row} from '../src/db.ts';
import {migrateSchema} from '../src/migrate.ts';
import {loadIdentity,fingerprint} from '../src/identity.ts';
import {Store} from '../src/store.ts';
import type {Config} from '../src/config.ts';
import type {PublishingEvent} from '../src/contracts.ts';
import {randomUUID} from 'node:crypto';
import type {MessageQueue,MessageQueueEnqueueOptions} from '@fedify/fedify';
export const actorId='https://federation.example/users/editor';
export const publicationId='publication-example';
export const remoteActor='https://remote.example/users/alice';
export const follower={actorId:remoteActor,inbox:remoteActor+'/inbox',sharedInbox:'https://remote.example/inbox'};
export class CaptureQueue implements MessageQueue{
 messages:{value:unknown;options?:MessageQueueEnqueueOptions}[]=[];
 async enqueue(value:unknown,options?:MessageQueueEnqueueOptions){this.messages.push({value,options});}
 handler?: (message:unknown)=>Promise<void>|void;
 async listen(handler:(message:unknown)=>Promise<void>|void){this.handler=handler;await this.drain();}
 async drain(){if(!this.handler)return;while(this.messages.length)await this.handler(this.messages.shift()!.value);}
}
export async function fixture(options:{publicationId?:string;actorId?:string}={}){
 const publicationId=options.publicationId??'publication-example';
 const actorId=options.actorId??'https://federation.example/users/editor';
 const origin=new URL(actorId).origin;
 const identifier=new URL(actorId).pathname.split('/').at(-1)!;
 const actorRoute=(new URL(actorId).pathname.slice(0,-identifier.length)+'{identifier}') as `${string}{identifier}`;
 const engine=new PGlite();
 await engine.exec('CREATE ROLE fixture_owner NOSUPERUSER; CREATE SCHEMA fixture_ap AUTHORIZATION fixture_owner; SET ROLE fixture_owner');
 const wrap=(c:Pick<PGlite,'query'|'transaction'>):Adapter=>({
 query:async<T extends Row>(text:string,params:unknown[]=[]) => (await c.query<T>(text,params)).rows,
 transaction:async<T>(work:(tx:Adapter)=>Promise<T>)=>c.transaction(tx=>work(wrap(tx as unknown as PGlite)))});
 const base=database(wrap(engine),'fixture_ap','fixture-installation');
 await migrateSchema(base);await base.query("INSERT INTO {{schema}}.publication_roles VALUES($1,current_user)",[publicationId]);const db=forPublication(base,publicationId);
 const keys=await crypto.subtle.generateKey({name:'RSASSA-PKCS1-v1_5',modulusLength:2048,publicExponent:new Uint8Array([1,0,1]),hash:'SHA-256'},true,['sign','verify']);
 const publicJwk=await crypto.subtle.exportKey('jwk',keys.publicKey),privateJwk=await crypto.subtle.exportKey('jwk',keys.privateKey);
 const config:Config={databaseUrl:'postgres://unused-test-fixture',schema:base.schema,installationId:base.installation,publicationId,actorId,keyId:actorId+'#main-key',origin,
 identifier,actorRoute,displayName:'Example Editor',profileUrl:'https://www.example.org/about',poolSize:5,
 expectedFingerprint:await fingerprint(keys.publicKey),publisherId:'ghost',publisherToken:'isolated-test-publisher-token-never-use-live',federationEnabled:true,host:'127.0.0.1',port:3000};
 const seedIdentity=async()=>db.query('INSERT INTO {{schema}}.identities(actor_id,key_id,public_jwk,private_jwk) VALUES($1,$2,$3,$4)',[actorId,config.keyId,publicJwk,privateJwk]);
 await seedIdentity();const identity=await loadIdentity(db,config),store=new Store(db,actorId);
 return {db,base,adapter:wrap(engine),engine,keys,publicJwk,privateJwk,config,state:{config,identity,store},store,close:()=>engine.close(),
 reset:async()=>{await engine.exec('TRUNCATE fixture_ap.feedback,fixture_ap.feedback_tombstones,fixture_ap.feedback_blocks,fixture_ap.content_objects,fixture_ap.identities,fixture_ap.inbox_receipts,fixture_ap.followers,fixture_ap.undone_follows,fixture_ap.delivery_failures,fixture_ap.worker_heartbeats,fixture_ap.transport_jobs,fixture_ap.federation_kv,fixture_ap.retired_objects,fixture_ap.object_id_reservations RESTART IDENTITY CASCADE');await seedIdentity();}};
}
export function published(overrides:Partial<Extract<PublishingEvent,{type:'content.published'}>>={}):Extract<PublishingEvent,{type:'content.published'}>{
 const contentId=overrides.content_id??randomUUID();
 return {schema_version:2,publication_id:publicationId,event_id:randomUUID(),content_id:contentId,object_instance:contentId,delivery_policy:'live',source:{id:'ghost',epoch:'1'},revision:'1',
 occurred_at:'2026-10-25T12:00:00Z',type:'content.published',document:{visibility:'public',title:'An article',html:'<p>Hello <strong>world</strong></p>',url:'https://federation.example/blog/article',published_at:'2026-10-25T12:00:00Z'},...overrides};
}
export function follow(id=remoteActor+'/follows/'+randomUUID()){return {id,actorId:remoteActor,target:actorId,actor:follower,follow:{'@context':'https://www.w3.org/ns/activitystreams',id,type:'Follow',actor:remoteActor,object:actorId}};}
