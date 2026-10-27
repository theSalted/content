import {test,mock} from 'node:test';
import {signRfc,verifyRfc,components,type RfcOptions} from './rfc9421.fixture.ts';
import assert from 'node:assert/strict';
import {randomUUID,createHash,createPublicKey,KeyObject,sign,verify} from 'node:crypto';
import dns from 'node:dns/promises';
import {request as httpRequest} from 'node:http';
import {serve} from '@hono/node-server';
import {getDocumentLoader} from '@fedify/vocab-runtime';
import {connect,forPublication} from '../src/db.ts';
import {migrateSchema} from '../src/migrate.ts';
import {importExistingIdentity} from '../src/import-identity.ts';
import {fingerprint} from '../src/identity.ts';
import {runtime} from '../src/runtime.ts';
import {makeFederation,enqueueIntent} from '../src/federation.ts';
import {makeApp} from '../src/app.ts';
import {DatabaseKvStore} from '../src/kv.ts';
import {DurableQueue} from '../src/queue.ts';
import {relayOne} from '../src/worker.ts';
import {published} from './helpers.ts';
import type {Config} from '../src/config.ts';
import type {ServerType} from '@hono/node-server';

const enabled=process.env.CONTENT_ACTIVITYPUB_PEER_TEST==='1'&&!!process.env.FEDERATION_TEST_DATABASE_URL;
const site='https://federation.example',peer='https://peer.example';
const actor=site+'/users/editor',peerActor=peer+'/users/alice';
const context=['https://www.w3.org/ns/activitystreams','https://w3id.org/security/v1'];
const digest=(body:Uint8Array)=>'SHA-256='+createHash('sha256').update(body).digest('base64');
const signatureFields=['(request-target)','host','date','digest'];
function signingText(url:URL,method:string,headers:Headers,fields:string[]){
 return fields.map(name=>name==='(request-target)'?`${name}: ${method.toLowerCase()} ${url.pathname}${url.search}`:`${name}: ${headers.get(name)}`).join('\n');
}
function signed(url:string,value:unknown,key:CryptoKey,keyId:string,date=new Date()):Request{
 const body=Buffer.from(JSON.stringify(value));const u=new URL(url);
 const headers=new Headers({'content-type':'application/activity+json',host:u.host,date:date.toUTCString(),digest:digest(body)});
 const sig=sign('RSA-SHA256',Buffer.from(signingText(u,'POST',headers,signatureFields)),KeyObject.from(key));
 headers.set('signature',`keyId="${keyId}",algorithm="rsa-sha256",headers="${signatureFields.join(' ')}",signature="${sig.toString('base64')}"`);
 return new Request(url,{method:'POST',headers,body});
}
const keyToJwk=new WeakMap<CryptoKey,JsonWebKey>();
async function keys(){
 const pair=await crypto.subtle.generateKey({name:'RSASSA-PKCS1-v1_5',modulusLength:2048,publicExponent:new Uint8Array([1,0,1]),hash:'SHA-256'},true,['sign','verify']);
 keyToJwk.set(pair.privateKey,await crypto.subtle.exportKey('jwk',pair.privateKey));return pair;
}
class SteppedQueue extends DurableQueue {
 handler?: (message:unknown)=>void|Promise<void>;
 override async listen(handler:(message:unknown)=>void|Promise<void>){this.handler=handler;}
 async step(){assert.ok(this.handler);return this.processOne(this.handler);}
}
async function listen(fetcher:(request:Request)=>Promise<Response>){
 let server!:ServerType;
 const url=await new Promise<string>(resolve=>{server=serve({hostname:'127.0.0.1',port:0,fetch:fetcher},info=>resolve(`http://127.0.0.1:${info.port}`));});
 return {url,close:()=>new Promise<void>((resolve,reject)=>server.close(error=>error?reject(error):resolve()))};
}

test('controlled HTTP peer with real PostgreSQL: signatures, lifecycle, retries and loader boundaries',{skip:!enabled,timeout:60000},async t=>{
 const url=new URL(process.env.FEDERATION_TEST_DATABASE_URL!);
 assert.ok(['localhost','127.0.0.1','[::1]'].includes(url.hostname));assert.ok(url.pathname.endsWith('_test'));
 const schema='peer_'+randomUUID().replaceAll('-',''),owner=schema+'_owner',role=schema+'_runtime';
 const admin=connect(url.href,schema),ownerUrl=new URL(url);ownerUrl.username=owner;
 await admin.db.query(`CREATE ROLE ${owner} LOGIN NOSUPERUSER NOBYPASSRLS`);
 await admin.db.query(`CREATE ROLE ${role} LOGIN NOSUPERUSER NOBYPASSRLS`);
 await admin.db.query(`CREATE SCHEMA ${schema} AUTHORIZATION ${owner}`);
 const migration=connect(ownerUrl.href,schema);let service:Awaited<ReturnType<typeof runtime>>|undefined;
 const servers:{close:()=>Promise<void>}[]=[];
 t.after(async()=>{mock.restoreAll();for(const s of servers)await s.close();await service?.close();await migration.close();
  await admin.db.query(`DROP SCHEMA ${schema} CASCADE`);await admin.db.query(`DROP ROLE ${role}`);await admin.db.query(`DROP ROLE ${owner}`);await admin.close();});
 await migrateSchema(migration.db);await migration.db.query('INSERT INTO {{schema}}.publication_roles VALUES($1,$2),($1,$3)',['controlled-peer',owner,role]);
 const localKeys=await keys(),remoteKeys=await keys();const runtimeUrl=new URL(url);runtimeUrl.username=role;
 const config:Config={databaseUrl:runtimeUrl.href,schema,installationId:'test-installation',publicationId:'controlled-peer',actorId:actor,keyId:actor+'#main-key',origin:site,
 identifier:'editor',actorRoute:'/users/{identifier}',displayName:'Controlled editor',profileUrl:site+'/about',expectedFingerprint:await fingerprint(localKeys.publicKey),
 publisherId:'content',publisherToken:'synthetic-controlled-peer-publisher-never-live',federationEnabled:true,host:'127.0.0.1',port:3000,poolSize:5};
 const ownerDB=forPublication(migration.db,config.publicationId);
 await importExistingIdentity(ownerDB,config,{actor_id:actor,key_id:config.keyId,public_jwk:await crypto.subtle.exportKey('jwk',localKeys.publicKey),private_jwk:keyToJwk.get(localKeys.privateKey)});
 await admin.db.query(`GRANT USAGE ON SCHEMA ${schema} TO ${role}`);
 await admin.db.query(`GRANT SELECT ON ${schema}.identities,${schema}.installation,${schema}.publication_roles TO ${role}`);
 const tables=['content_objects','publishing_events','activities','inbox_receipts','followers','undone_follows','outbound_intents','delivery_failures','worker_heartbeats','transport_jobs','federation_kv','retired_objects'];
 await admin.db.query(`GRANT SELECT,INSERT,UPDATE,DELETE ON ${tables.map(n=>schema+'.'+n).join(',')} TO ${role}`);
 await admin.db.query(`GRANT SELECT,INSERT ON ${schema}.object_id_reservations TO ${role}`);
 await admin.db.query(`GRANT USAGE ON ALL SEQUENCES IN SCHEMA ${schema} TO ${role}`);
 service=await runtime(config);const state=service.state,db=state.store.db;
 const queue=new SteppedQueue(db),federation=makeFederation(state,new DatabaseKvStore(db),queue);const app=makeApp(state,federation);
 await federation.startQueue(state); // Register the real Fedify handler, step durable transactions deterministically.
 const siteServer=await listen(app);servers.push(siteServer);
 const remotePublicPem=KeyObject.from(remoteKeys.publicKey).export({type:'spki',format:'pem'}).toString();
 const remoteDocument={'@context':context,id:peerActor,type:'Person',preferredUsername:'alice',inbox:peer+'/inbox',publicKey:{id:peerActor+'#main-key',owner:peerActor,publicKeyPem:remotePublicPem}};
 let peerFormat:'legacy'|'rfc9421'='legacy';
 const modernReceived:Record<string,any>[]=[];
 const rejected:unknown[]=[];
 const attempts:{id:string;type:string;signatureValid:boolean;body:string}[]=[],applied=new Map<string,Record<string,any>>(),duplicates:string[]=[];
 let failBeforeType:string|undefined,commitThenFailType:string|undefined;
 const peerObjects=new Map<string,Record<string,unknown>|null>();
 const actorReads={site:0,peer:0};const dispatched:string[]=[];
 let peerServerUrl='';
 const peerServer=await listen(async req=>{
  const path=new URL(req.url).pathname;
  if(path==='/users/alice'){actorReads.peer++;return Response.json(remoteDocument,{headers:{'content-type':'application/activity+json'}});}
  if(path==='/redirect-private')return new Response(null,{status:302,headers:{location:'http://127.0.0.1:9/forbidden'}});
  if(path==='/alternate-private')return new Response('<html></html>',{headers:{'content-type':'text/html',link:'<http://127.0.0.1:9/forbidden>; rel="alternate"; type="application/activity+json"'}});
  if(path==='/inbox-redirect')return new Response(null,{status:307,headers:{location:'https://127.0.0.1:9/forbidden'}});
  if(path!=='/inbox'||req.method!=='POST')return new Response(null,{status:404});
  const body=Buffer.from(await req.arrayBuffer());
  if(peerFormat==='rfc9421'){
   const doc=await (await fetch(actor,{headers:{accept:'application/activity+json'}})).json();actorReads.site++;
   if(!verifyRfc(peer+'/inbox',req.method,req.headers,body,doc.publicKey,actor))return new Response(null,{status:401});
   modernReceived.push(JSON.parse(body.toString()));return new Response(null,{status:202});
  }
  const fields=Object.fromEntries([... (req.headers.get('signature')??'').matchAll(/(\w+)="([^"]*)"/g)].map(m=>[m[1]!,m[2]!]));
  const names=(fields.headers??'').split(' ');const timestamp=Date.parse(req.headers.get('date')??'');
  if(!signatureFields.every(n=>names.includes(n))||new Set(names).size!==names.length||!Number.isFinite(timestamp)||Math.abs(Date.now()-timestamp)>3600000||req.headers.get('digest')!==digest(body)||fields.keyId!==config.keyId){rejected.push({step:'headers',names,keyId:fields.keyId,host:req.headers.get('host'),digestOK:req.headers.get('digest')===digest(body),date:req.headers.get('date'),headerNames:[...req.headers.keys()]});return new Response(null,{status:401});}
  // Independently obtain the sender's public PEM over actual loopback HTTP.
  const actorResponse=await fetch(actor,{headers:{accept:'application/activity+json'}});actorReads.site++;
  const document=await actorResponse.json();const publicKey=document.publicKey;
  const good=publicKey?.id===fields.keyId&&publicKey.owner===actor&&verify('RSA-SHA256',Buffer.from(signingText(new URL(peer+'/inbox'),req.method,req.headers,names)),createPublicKey(publicKey.publicKeyPem),Buffer.from(fields.signature??'','base64'));
  if(!good){rejected.push({step:'rsa',host:req.headers.get('host'),names,algorithm:fields.algorithm,keyId:fields.keyId,publishedId:publicKey?.id});return new Response(null,{status:401});}
  const activity=JSON.parse(body.toString());attempts.push({id:activity.id,type:activity.type,signatureValid:good,body:body.toString()});
  if(failBeforeType===activity.type){failBeforeType=undefined;return new Response('synthetic transient failure',{status:503});}
  if(applied.has(activity.id)){duplicates.push(activity.id);return new Response(null,{status:202});}
  applied.set(activity.id,activity);
  if(activity.type==='Create'||activity.type==='Update')peerObjects.set(activity.object.id,activity.object);
  if(activity.type==='Delete')peerObjects.set(typeof activity.object==='string'?activity.object:activity.object.id,null);
  if(commitThenFailType===activity.type){commitThenFailType=undefined;return new Response('synthetic lost acknowledgment',{status:503});}
  return new Response(null,{status:202});
 });servers.push(peerServer);peerServerUrl=peerServer.url;
 // Test-only virtual public network: the real default URL/DNS/redirect guards
 // still run. DNS answers and final sockets are substituted only here. Never
 // contact the synthetic public IP or any unlisted origin; no OS DNS changes.
 mock.method(dns,'lookup',async(host:string)=>{
  if(['federation.example','peer.example'].includes(host))return [{address:'93.184.216.34',family:4}];
  if(host==='private-dns.example')return [{address:'127.0.0.1',family:4}];
  if(host==='mixed-dns.example')return [{address:'93.184.216.34',family:4},{address:'10.0.0.1',family:4}];
  throw new Error('Unexpected synthetic DNS lookup: '+host);
 });
 mock.method(globalThis,'fetch',async(input:RequestInfo|URL,init?:RequestInit)=>{
  const req=new Request(input,init),u=new URL(req.url);dispatched.push(u.href);
  const destination=u.origin===site?siteServer.url:u.origin===peer?peerServerUrl:undefined;
  if(!destination)throw new Error('External/private dispatch prohibited by test harness: '+u.origin);
  const headers=new Headers(req.headers);headers.set('host',u.host);
  const body=['GET','HEAD'].includes(req.method)?undefined:Buffer.from(await req.arrayBuffer());
  return new Promise<Response>((resolve,reject)=>{
   const outgoing=httpRequest(destination+u.pathname+u.search,{method:req.method,headers:Object.fromEntries(headers),signal:req.signal},response=>{
    const chunks:Buffer[]=[];response.on('data',chunk=>chunks.push(Buffer.from(chunk)));response.on('error',reject);
    response.on('end',()=>{const returned=new Headers();for(const [key,value] of Object.entries(response.headers)){
     if(Array.isArray(value))for(const v of value)returned.append(key,v);else if(value!==undefined)returned.set(key,value);
    }resolve(new Response(Buffer.concat(chunks),{status:response.statusCode!,headers:returned}));});
   });outgoing.on('error',reject);outgoing.end(body);
  });
 });
 const follow={'@context':context,id:peerActor+'/follows/'+randomUUID(),type:'Follow',actor:peerActor,object:actor};
 const inbox=(value:unknown,date?:Date)=>fetch(signed(actor+'/inbox',value,remoteKeys.privateKey,peerActor+'#main-key',date));
 const relay=()=>relayOne(state.store,job=>enqueueIntent(federation,state,job));
 const fastForward=()=>ownerDB.query('UPDATE {{schema}}.transport_jobs SET available_at=clock_timestamp() WHERE NOT dead');
 const publish=async(event:unknown)=>{
  const res=await fetch(site+'/internal/v2/events',{method:'POST',headers:{authorization:'Bearer '+config.publisherToken,'content-type':'application/json'},body:JSON.stringify(event)});
  assert.equal(res.status,202);return res.json();
 };
 await t.test('independent peer signatures enforce digest and past/future time bounds before queue acceptance',async()=>{
  for(const delta of [-7200000,7200000]){const res=await inbox({...follow,id:follow.id+delta},new Date(Date.now()+delta));assert.ok([400,401,403].includes(res.status));}
  const signedRequest=signed(actor+'/inbox',follow,remoteKeys.privateKey,peerActor+'#main-key');
  const tampered=new Request(signedRequest.url,{method:'POST',headers:signedRequest.headers,body:JSON.stringify({...follow,type:'Like'})});
  assert.ok([400,401,403].includes((await fetch(tampered)).status));
  assert.equal((await queue.getDepth()).queued,0);
 });
 await t.test('signed Follow is queued, Accept is delivered over HTTP and duplicate Follow is harmless',async()=>{
  assert.equal((await inbox(follow)).status,202);assert.equal((await state.store.followers(0)).length,0);
  assert.equal(await queue.step(),true);assert.equal((await state.store.followers(0)).length,1);
  assert.equal(await relay(),true);assert.equal(await queue.step(),true);
  const accepts=[...applied.values()].filter(a=>a.type==='Accept');assert.equal(accepts.length,1,JSON.stringify({rejected,dispatched}));assert.equal(accepts[0]!.object.id,follow.id);
  assert.ok(actorReads.peer>0&&actorReads.site>0);assert.ok(rejected.some((item:any)=>item.headerNames?.includes('signature-input')),'Default RFC9421 first attempt fell back to peer-supported Cavage');assert.equal((await inbox(follow)).status,202);await queue.step();
  assert.equal((await db.query("SELECT id FROM {{schema}}.activities WHERE kind='Accept'")).length,1);assert.equal(await relay(),false);
 });
 const initial=published({publication_id:config.publicationId,source:{id:'content',epoch:'1'}});let objectId='';
 await t.test('Create retry preserves identity after peer commit/lost acknowledgment; Update cannot overtake',async()=>{
  objectId=(await publish(initial)).object_id;commitThenFailType='Create';assert.equal(await relay(),true);await queue.step();
  const create=[...applied.values()].find(a=>a.type==='Create');assert.ok(create);assert.equal(create.object.id,objectId);
  const jobs=await db.query<{attempts:number}>('SELECT attempts FROM {{schema}}.transport_jobs');assert.equal(jobs.length,1);assert.equal(jobs[0]!.attempts,1);
  const update={...initial,event_id:randomUUID(),revision:'2',type:'content.updated',document:{...initial.document,title:'Updated title',html:'<p>Updated body</p>'}};
  await publish(update);assert.equal(await relay(),true);assert.equal(await queue.step(),false);
  await fastForward();assert.equal(await queue.step(),true);assert.ok(duplicates.includes(create.id));
  const retried=attempts.filter(a=>a.id===create.id);assert.equal(retried.length,2);assert.equal(retried[0]!.body,retried[1]!.body);
  failBeforeType='Update';await queue.step();assert.equal([...applied.values()].filter(a=>a.type==='Update').length,0);
  await fastForward();await queue.step();const updated=[...applied.values()].find(a=>a.type==='Update');assert.equal(updated?.object.id,objectId);assert.equal(updated?.object.name,'Updated title');assert.equal(peerObjects.get(objectId)?.name,'Updated title');
  const failures=await db.query<{error_code:string}>('SELECT error_code FROM {{schema}}.delivery_failures');
  assert.ok(failures.length>=2&&failures.every(row=>/^http_/.test(row.error_code)),JSON.stringify(failures));
 });
 await t.test('withdrawal delivers signed Delete and dereferences as a tombstone',async()=>{
  const {document,...base}=initial;await publish({...base,event_id:randomUUID(),revision:'3',type:'content.withdrawn'});
  await relay();await queue.step();const deleted=[...applied.values()].find(a=>a.type==='Delete');assert.equal(deleted?.object,objectId);assert.equal(peerObjects.get(objectId),null);
  const res=await fetch(objectId,{headers:{accept:'application/activity+json'}});assert.equal(res.status,410);assert.equal((await res.json()).type,'Tombstone');
  assert.deepEqual([...applied.values()].map(a=>a.type),['Accept','Create','Update','Delete']);assert.ok(attempts.every(a=>a.signatureValid));
 });
 await t.test('signed Undo removes only the authenticated follow and duplicate Undo is harmless',async()=>{
  const before=(await queue.getDepth()).queued;assert.ok([400,401,403].includes((await inbox({...follow,id:follow.id+'forged',actor:peer+'/users/bob'})).status));assert.equal((await queue.getDepth()).queued,before);
  const undo={'@context':context,id:peerActor+'/undo/'+randomUUID(),type:'Undo',actor:peerActor,object:follow};
  assert.equal((await inbox(undo)).status,202);await queue.step();assert.equal((await state.store.followers(0)).length,0);
  assert.equal((await inbox(undo)).status,202);await queue.step();assert.equal((await state.store.followers(0)).length,0);
 });
 await t.test('RFC9421 invalid coverage, digest, time and key ownership are rejected before durable acceptance',async()=>{
  const now=Math.floor(Date.now()/1000),cases:[string,RfcOptions,Record<string,unknown>?][]=[
   ...components.map(c=>['missing '+c,{components:components.filter(n=>n!==c)}] as [string,RfcOptions]),
   ['past created',{created:now-7200}],['future created',{created:now+7200}],['missing created',{created:null}],
   ['expired',{created:now-120,expires:now-60}],['expiry before creation',{created:now+60,expires:now+30}],['bad digest',{digest:'sha-256=:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=:'}],
   ['wrong target',{signUrl:site+'/inbox'}],['wrong method',{signMethod:'GET'}],
   ['wrong actor',{}, {actor:peer+'/users/bob'}],['wrong algorithm',{algorithm:'rsa-pss-sha512'}],['duplicate components',{components:[...components,'content-digest']}]
  ];
  const observations:Record<string,number>={};const before=(await queue.getDepth()).queued;
  for(const [name,options,change] of cases){const value={...follow,id:peerActor+'/negative/'+randomUUID(),...change};observations[name]=(await fetch(signRfc(actor+'/inbox',value,remoteKeys.privateKey,peerActor+'#main-key',options))).status;}
  const value={...follow,id:peerActor+'/negative/'+randomUUID()};
  const current=()=>signRfc(actor+'/inbox',value,remoteKeys.privateKey,peerActor+'#main-key');
  const changedBody=current();observations['body changed after signing']=(await fetch(new Request(changedBody.url,{method:'POST',headers:changedBody.headers,body:JSON.stringify({...value,type:'Undo'})}))).status;
  const missingDigest=current();missingDigest.headers.delete('content-digest');observations['missing digest header']=(await fetch(missingDigest)).status;
  const changedDigest=current();changedDigest.headers.set('content-digest','sha-256=:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=:');observations['digest changed after signing']=(await fetch(changedDigest)).status;
  observations['wrong signing key']=(await fetch(signRfc(actor+'/inbox',value,localKeys.privateKey,peerActor+'#main-key'))).status;
  observations['key ID not published by owner']=(await fetch(signRfc(actor+'/inbox',value,remoteKeys.privateKey,peerActor+'#not-owned'))).status;
  const mismatched=current();mismatched.headers.set('signature',mismatched.headers.get('signature')!.replace('peer=', 'different='));observations['label mismatch']=(await fetch(mismatched)).status;
  const fresh=current();fresh.headers.set('signature','peer=:AQID:');
  const expired=signRfc(actor+'/inbox',value,remoteKeys.privateKey,peerActor+'#main-key',{label:'expired',created:now-120,expires:now-60});
  fresh.headers.set('signature-input',expired.headers.get('signature-input')+', '+fresh.headers.get('signature-input'));
  fresh.headers.set('signature',expired.headers.get('signature')+', '+fresh.headers.get('signature'));
  observations['expired valid plus fresh invalid signature']=(await fetch(fresh)).status;
  // Keep a failing reproduction isolated; never process an unexpectedly accepted negative fixture.
  const after=(await queue.getDepth()).queued;
  if(after!==before)await ownerDB.query('DELETE FROM {{schema}}.transport_jobs');
  assert.ok(Object.values(observations).every(s=>[400,401,403].includes(s)),JSON.stringify(observations));assert.equal(after,before);
 });
 await t.test('independent RFC9421 Follow/Accept/Undo succeeds including optional alg and arbitrary label',async()=>{
  peerFormat='rfc9421';
  for(const options of [{label:'interop'}, {label:'mastodon',algorithm:null,expires:Math.floor(Date.now()/1000)+120}] satisfies RfcOptions[]){
   const value={...follow,id:peerActor+'/modern/'+randomUUID()};
   const request=signRfc(actor+'/inbox',value,remoteKeys.privateKey,peerActor+'#main-key',options);
   assert.equal((await fetch(request)).status,202);await queue.step();assert.equal((await state.store.followers(0)).length,1);
   await relay();await queue.step();assert.equal(modernReceived.at(-1)?.object.id,value.id);
   assert.equal((await fetch(signRfc(actor+'/inbox',value,remoteKeys.privateKey,peerActor+'#main-key',options))).status,202);await queue.step();assert.equal(await relay(),false);
   if(options.label==='interop'){
    const post=published({publication_id:config.publicationId,source:{id:'content',epoch:'1'}});
    const id=(await publish(post)).object_id;await relay();await queue.step();
    await publish({...post,event_id:randomUUID(),revision:'2',type:'content.updated',document:{...post.document,title:'RFC9421 updated'}});await relay();await queue.step();
    const {document,...base}=post;await publish({...base,event_id:randomUUID(),revision:'3',type:'content.withdrawn'});await relay();await queue.step();
    assert.deepEqual(modernReceived.slice(1).map(a=>a.type),['Create','Update','Delete']);
    assert.equal(modernReceived.at(-1)?.object,id);
   }
   const undo={'@context':context,id:peerActor+'/modern-undo/'+randomUUID(),type:'Undo',actor:peerActor,object:value};
   assert.equal((await fetch(signRfc(actor+'/inbox',undo,remoteKeys.privateKey,peerActor+'#main-key',options))).status,202);await queue.step();assert.equal((await state.store.followers(0)).length,0);
  }
  assert.equal(modernReceived.length,5);peerFormat='legacy';
 });
 await t.test('actual default loader blocks private IPs, DNS results, credential URLs and private redirects',async()=>{
  const loader=getDocumentLoader({timeout:2000});
  for(const address of ['http://127.0.0.1:9/private','http://[::1]:9/private','http://169.254.169.254/private','http://10.0.0.1/private','http://localhost/private','https://private-dns.example/private','https://mixed-dns.example/private','https://fake:fake@peer.example/private','file:///tmp/not-read']){
   const count=dispatched.length;await assert.rejects(loader(address));assert.equal(dispatched.length,count,address+' must fail before fetch');
  }
  for(const path of ['/redirect-private','/alternate-private']){const count=dispatched.length;await assert.rejects(loader(peer+path));assert.deepEqual(dispatched.slice(count),[peer+path]);}
 });
 await t.test('outbound redirect to private address is rejected before dispatch and retained for retry',async()=>{
  await state.store.receiveFollow({id:peerActor+'/follows/redirect',actorId:peerActor,target:actor,actor:{actorId:peerActor,inbox:peer+'/inbox-redirect'},follow:{...follow,id:peerActor+'/follows/redirect'}});
  await relay();const count=dispatched.length;await queue.step();assert.deepEqual(dispatched.slice(count),[peer+'/inbox-redirect']);
  const jobs=await db.query<{attempts:number}>('SELECT attempts FROM {{schema}}.transport_jobs');assert.equal(jobs.length,1);assert.equal(jobs[0]!.attempts,1);
 });
});
