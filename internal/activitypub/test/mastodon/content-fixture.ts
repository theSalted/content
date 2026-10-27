// Synthetic integration endpoint only. Excluded from production Docker context.
import assert from 'node:assert/strict';
import {mock} from 'node:test';
import {randomUUID,createHash} from 'node:crypto';
import {readFile} from 'node:fs/promises';
import dns from 'node:dns/promises';
import {createServer as httpsServer,request as httpsRequest} from 'node:https';
import {createServer as httpServer,request as httpRequest} from 'node:http';
import {connect as socketConnect} from 'node:net';
import {serve} from '@hono/node-server';
import {connect,forPublication} from '../../src/db.ts';
import {migrateSchema} from '../../src/migrate.ts';
import {importExistingIdentity} from '../../src/import-identity.ts';
import {fingerprint} from '../../src/identity.ts';
import {runtime} from '../../src/runtime.ts';
import {makeFederation,enqueueIntent} from '../../src/federation.ts';
import {makeApp} from '../../src/app.ts';
import {DatabaseKvStore} from '../../src/kv.ts';
import {DurableQueue} from '../../src/queue.ts';
import {relayOne} from '../../src/worker.ts';
import type {Config} from '../../src/config.ts';
import type {PublishingEvent} from '../../src/contracts.ts';

assert.equal(process.env.CONTENT_MASTODON_FIXTURE,'synthetic-only');
assert.ok(process.env.FIXTURE_CONTROL_TOKEN);
const site='https://federation.example',peer='https://mastodon.example',actor=site+'/users/editor';
const url=new URL('postgres://postgres@pg:5432/content_mastodon_test');
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
 const schema='masto_'+randomUUID().replaceAll('-',''),owner=schema+'_owner',role=schema+'_runtime';
 const admin=connect(url.href,schema),ownerUrl=new URL(url);ownerUrl.username=owner;
 await admin.db.query(`CREATE ROLE ${owner} LOGIN NOSUPERUSER NOBYPASSRLS`);
 await admin.db.query(`CREATE ROLE ${role} LOGIN NOSUPERUSER NOBYPASSRLS`);
 await admin.db.query(`CREATE SCHEMA ${schema} AUTHORIZATION ${owner}`);
 const migration=connect(ownerUrl.href,schema);let service:Awaited<ReturnType<typeof runtime>>|undefined;


 await migrateSchema(migration.db);await migration.db.query('INSERT INTO {{schema}}.publication_roles VALUES($1,$2),($1,$3)',['mastodon-peer',owner,role]);
 const localKeys=await keys();const runtimeUrl=new URL(url);runtimeUrl.username=role;
 const config:Config={databaseUrl:runtimeUrl.href,schema,installationId:'test-installation',publicationId:'mastodon-peer',actorId:actor,keyId:actor+'#main-key',origin:site,
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


const cert=await readFile('/fixture/cert.pem'),key=await readFile('/fixture/key.pem');
const incoming:{body:string;headers:Headers;url:string}[]=[],discovery:string[]=[];
const trace:{path:string;status:number;detail?:string}[]=[];
const deliveries:{type:string;format:string;status:number;id:string;bodyHash:string}[]=[],sent:Request[]=[];
let failCreate=false;
// Actual HTTPS termination, with canonical Host preserved into the real peer.
async function peerHttp(req:Request):Promise<Response>{
 const u=new URL(req.url),headers=new Headers(req.headers);headers.set('host','mastodon.example');headers.set('x-forwarded-proto','https');
 const body=['GET','HEAD'].includes(req.method)?undefined:Buffer.from(await req.arrayBuffer());
 if(body&&JSON.parse(body.toString()).type==='Create'&&failCreate)return new Response('synthetic ingress failure',{status:503});
 return new Promise((resolve,reject)=>{
  const outgoing=httpRequest('http://mastodon-web:3000'+u.pathname+u.search,{method:req.method,headers:Object.fromEntries(headers)},response=>{
   const chunks:Buffer[]=[];response.on('data',c=>chunks.push(Buffer.from(c)));response.on('error',reject);response.on('end',()=>{
    const returned=new Headers();for(const [name,value] of Object.entries(response.headers)){if(Array.isArray(value))for(const v of value)returned.append(name,v);else if(value!==undefined)returned.set(name,value);}
    resolve(new Response(Buffer.concat(chunks),{status:response.statusCode!,headers:returned}));
   });
  });outgoing.on('error',reject);outgoing.end(body);
 });
}
serve({hostname:'0.0.0.0',port:8443,createServer:httpsServer,serverOptions:{key,cert},fetch:async req=>{
 const host=req.headers.get('host');
 if(host==='mastodon.example')return peerHttp(req);
 if(host!=='federation.example')return new Response(null,{status:421});
 discovery.push(new URL(req.url).pathname);
 if(req.method==='POST')incoming.push({body:await req.clone().text(),headers:new Headers(req.headers),url:req.url});
 const response=await app(req);
 trace.push({path:new URL(req.url).pathname,status:response.status,...(response.status>=400?{detail:(await response.clone().text()).slice(0,300)}:{})});
 return response;
}});
const tunnels:string[]=[];
const proxy=httpServer((_req,res)=>{res.writeHead(403);res.end();});
proxy.on('connect',(req,client,head)=>{
 tunnels.push(req.url??'');
 if(!['federation.example:443','mastodon.example:443'].includes(req.url??'')){client.end('HTTP/1.1 403 Forbidden\r\n\r\n');return;}
 const upstream=socketConnect(8443,'127.0.0.1',()=>{client.write('HTTP/1.1 200 Connection Established\r\n\r\n');if(head.length)upstream.write(head);client.pipe(upstream);upstream.pipe(client);});
 upstream.on('error',()=>client.destroy());client.on('error',()=>upstream.destroy());client.on('close',()=>upstream.destroy());
});
proxy.listen(8080,'0.0.0.0');
// Test-only mapping at DNS/transport boundary; production default URL, DNS
// classification and redirect checks still execute. No public address is contacted.
mock.method(dns,'lookup',async(host:string)=>{if(['federation.example','mastodon.example'].includes(host))return [{address:'93.184.216.34',family:4}];throw Error('External DNS prohibited: '+host);});
mock.method(globalThis,'fetch',async(input:RequestInfo|URL,init?:RequestInit)=>{
 const req=new Request(input,init),u=new URL(req.url);assert.equal(u.origin,peer,'Unlisted origin refused by fixture transport');
 if(req.method==='POST')sent.push(req.clone());
 const body=['GET','HEAD'].includes(req.method)?undefined:Buffer.from(await req.arrayBuffer());
 const activity=body?JSON.parse(body.toString()):undefined;
 const headers=new Headers(req.headers);headers.set('host',u.host);
 return new Promise<Response>((resolve,reject)=>{
  const outgoing=httpsRequest({hostname:'127.0.0.1',port:8443,servername:u.hostname,ca:cert,path:u.pathname+u.search,method:req.method,headers:Object.fromEntries(headers),signal:req.signal},response=>{
   const chunks:Buffer[]=[];response.on('data',c=>chunks.push(Buffer.from(c)));response.on('error',reject);response.on('end',()=>{
    const returned=new Headers();for(const [name,value] of Object.entries(response.headers)){if(Array.isArray(value))for(const v of value)returned.append(name,v);else if(value!==undefined)returned.set(name,value);}
    trace.push({path:'peer:'+u.pathname,status:response.statusCode!,...(response.statusCode!>=400?{detail:Buffer.concat(chunks).toString().slice(0,300)}:{})});
    if(activity)deliveries.push({type:activity.type,format:req.headers.has('signature-input')?'rfc9421':'cavage',status:response.statusCode!,id:activity.id,bodyHash:createHash('sha256').update(body!).digest('hex')});
    resolve(new Response(Buffer.concat(chunks),{status:response.statusCode!,headers:returned}));
   });
  });outgoing.on('error',reject);outgoing.end(body);
 });
});
// Task-private orchestration API; never included in the production application.
serve({hostname:'0.0.0.0',port:8081,fetch:async req=>{
 if(req.headers.get('authorization')!=='Bearer '+process.env.FIXTURE_CONTROL_TOKEN)return new Response(null,{status:401});
 try{
  const {action,event,id}=await req.json() as {action:string;event?:PublishingEvent;id?:string};
  let result:unknown;
  switch(action){
   case 'snapshot':result={followers:(await state.store.followers(0)).length,queue:await queue.getDepth(),trace,deliveries,discovery,incoming:incoming.map(r=>JSON.parse(r.body).type),tunnels};break;
   case 'publish':assert.ok(event);result=await state.store.publish(event,config.publisherId);break;
   case 'step':result=await queue.step();break;
   case 'relay':result=await relayOne(state.store,job=>enqueueIntent(federation,state,job));break;
   case 'advance':await ownerDB.query('UPDATE {{schema}}.transport_jobs SET available_at=clock_timestamp()');result=true;break;
   case 'fail-create':failCreate=true;result=true;break;
   case 'allow-create':failCreate=false;result=true;break;
   case 'replay-create':{
    const originals=await Promise.all(sent.map(async r=>({r,body:await r.clone().json()})));
    const first=originals.find(r=>r.body.type==='Create');assert.ok(first);result=(await fetch(first.r.clone())).status;break;
   }
   case 'replay-follow':
   case 'replay-undo':{
    const first=incoming.find(r=>JSON.parse(r.body).type===(action==='replay-follow'?'Follow':'Undo'));assert.ok(first);
    result=await new Promise<number>((resolve,reject)=>{
     const replay=httpsRequest({hostname:'127.0.0.1',port:8443,servername:'federation.example',ca:cert,path:new URL(first.url).pathname,method:'POST',headers:Object.fromEntries(first.headers)},res=>{res.resume();res.on('end',()=>resolve(res.statusCode!));});
     replay.on('error',reject);replay.end(first.body);
    });break;
   }
   case 'peer-status':{assert.ok(id&&/^[0-9]+$/.test(id));const response=await fetch(peer+'/api/v1/statuses/'+id);result={status:response.status,body:await response.json()};break;}
   case 'object':assert.ok(id);result=await (await app(new Request(id,{headers:{accept:'application/activity+json'}}))).json();break;
   case 'tombstone':assert.ok(id);result=(await app(new Request(id,{headers:{accept:'application/activity+json'}}))).status;break;
   default:return new Response(null,{status:400});
  }
  return Response.json({result});
 }catch(error){console.error(error instanceof Error?error.message:'Fixture command failed');return Response.json({error:'fixture_command_failed'},{status:500});}
}});
console.log('SYNTHETIC_CONTENT_FIXTURE_READY');
