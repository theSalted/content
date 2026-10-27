// Explicit disposable cloud fixture. Never the normal application entrypoint.
// No DNS/socket/SSRF overrides, additional service, stored peer key or external account.
import assert from 'node:assert/strict';
import {randomUUID,createHash,KeyObject,sign,verify,createPublicKey,timingSafeEqual} from 'node:crypto';
import {serve} from '@hono/node-server';
import {createModule} from './src/module.ts';
import {splitIngress} from './src/ingress.ts';
import {processSettings,shutdownDeadline} from './src/process-settings.ts';
import {signRfc,verifyRfc} from './rfc9421.fixture.ts';

assert.equal(process.env.CONTENT_STAGING_PEER_ENABLED,'true');
assert.equal(process.env.FEDERATION_ENABLED,'true');
assert.equal(process.env.PUBLICATION_ID,process.env.CONTENT_STAGING_PUBLICATION);
const actor=process.env.ACTOR_ID,origin=new URL(actor).origin;
assert.equal(origin,process.env.CONTENT_STAGING_PUBLIC_ORIGIN);
assert.equal(new URL(origin).protocol,'https:');
const settings=processSettings(process.env);assert.ok(settings.internalPort);
const privateBase='http://127.0.0.1:'+settings.internalPort;
const token=process.env.PUBLISHER_TOKEN;assert.ok(token&&token.length>=32);
const goMode=process.env.CONTENT_STAGING_PRODUCER==='go',goController=new AbortController();
const runId=randomUUID(),peerActor=origin+'/_staging/peer/'+runId;
const peerInbox=peerActor+'/inbox',peerKeyId=peerActor+'#main-key';
const context=['https://www.w3.org/ns/activitystreams','https://w3id.org/security/v1'];
const pair=await crypto.subtle.generateKey({name:'RSASSA-PKCS1-v1_5',hash:'SHA-256',modulusLength:2048,publicExponent:new Uint8Array([1,0,1])},true,['sign','verify']);
const peerDocument={'@context':context,id:peerActor,type:'Person',preferredUsername:'disposable-peer',inbox:peerInbox,publicKey:{id:peerKeyId,owner:peerActor,publicKeyPem:KeyObject.from(pair.publicKey).export({type:'spki',format:'pem'}).toString()}};
const report={cmsAttempts:[],runId,state:'waiting',startedAt:new Date().toISOString(),checks:[],attempts:[],applied:[],duplicates:[],peerIdentity:peerActor,scope:'Controlled peer through real public HTTPS; not a cloud Mastodon instance'};
const applied=new Map(),objects=new Map();let failCreateRemaining=2,draining=false;
const http=(url,init={})=>fetch(url,{...init,signal:AbortSignal.timeout(15000)});
const authHeaders={'content-type':'application/json',authorization:'Bearer '+token};
const safeError=()=>new Response(null,{status:404,headers:{'cache-control':'no-store'}});
const digest=body=>'SHA-256='+createHash('sha256').update(body).digest('base64');
function legacyText(url,method,headers,names){const u=new URL(url);return names.map(n=>n==='(request-target)'?n+': '+method.toLowerCase()+' '+u.pathname+u.search:n+': '+headers.get(n)).join('\n');}
function legacy(value){const url=actor+'/inbox',body=Buffer.from(JSON.stringify(value)),names=['(request-target)','host','date','digest'];
 const headers=new Headers({'content-type':'application/activity+json',host:new URL(url).host,date:new Date().toUTCString(),digest:digest(body)});
 const signature=sign('RSA-SHA256',Buffer.from(legacyText(url,'POST',headers,names)),KeyObject.from(pair.privateKey));
 headers.set('signature',`keyId="${peerKeyId}",algorithm="rsa-sha256",headers="${names.join(' ')}",signature="${signature.toString('base64')}"`);
 return new Request(url,{method:'POST',headers,body});
}
async function peer(req){
 const path=new URL(req.url).pathname;
 if(path==='/_staging/report'&&req.method==='GET')return Response.json(report,{headers:{'cache-control':'no-store'}});
 if(path===new URL(peerActor).pathname&&req.method==='GET')return Response.json(peerDocument,{headers:{'content-type':'application/activity+json','cache-control':'no-store'}});
 if(path!==new URL(peerInbox).pathname||req.method!=='POST')return safeError();
 const reader=req.body?.getReader();if(!reader)return new Response(null,{status:400});
 const chunks=[];let size=0;for(;;){const r=await reader.read();if(r.done)break;size+=r.value.byteLength;if(size>2*1024*1024){await reader.cancel();return new Response(null,{status:413});}chunks.push(r.value);}
 const body=Buffer.concat(chunks),document=await (await http(actor,{headers:{accept:'application/activity+json'}})).json();
 const headers=new Headers(req.headers);headers.set('host',new URL(origin).host);
 let valid=false,format='rfc9421';
 if(headers.has('signature-input'))valid=verifyRfc(peerInbox,req.method,headers,body,document.publicKey,actor);
 else{format='legacy';const fields=Object.fromEntries([...(headers.get('signature')??'').matchAll(/(\w+)="([^"]*)"/g)].map(m=>[m[1],m[2]])),names=(fields.headers??'').split(' '),time=Date.parse(headers.get('date')??'');
  valid=['(request-target)','host','date','digest'].every(n=>names.includes(n))&&new Set(names).size===names.length&&Number.isFinite(time)&&Math.abs(Date.now()-time)<3600000&&headers.get('digest')===digest(body)&&fields.keyId===document.publicKey.id&&document.publicKey.owner===actor&&verify('RSA-SHA256',Buffer.from(legacyText(peerInbox,req.method,headers,names)),createPublicKey(document.publicKey.publicKeyPem),Buffer.from(fields.signature??'','base64'));
 }
 if(!valid)return new Response(null,{status:401});
 const activity=JSON.parse(body);if(activity.actor!==actor)return new Response(null,{status:403});
 report.attempts.push({id:activity.id,type:activity.type,format,bodySha256:createHash('sha256').update(body).digest('hex')});
 if(applied.has(activity.id)){report.duplicates.push(activity.id);if(activity.type==='Create'&&failCreateRemaining>0){failCreateRemaining--;return new Response('synthetic lost acknowledgment',{status:503});}return new Response(null,{status:202});}
 applied.set(activity.id,activity);report.applied.push({id:activity.id,type:activity.type});
 if(['Create','Update'].includes(activity.type))objects.set(activity.object.id,activity.object);
 if(activity.type==='Delete')objects.set(typeof activity.object==='string'?activity.object:activity.object.id,null);
 if(activity.type==='Create'&&failCreateRemaining>0){failCreateRemaining--;return new Response('synthetic lost acknowledgment',{status:503});}
 return new Response(null,{status:202});
}
const app=await createModule(process.env),ingress=splitIngress(app.fetch,true);
let lostGoAck=false;
const authorized=req=>timingSafeEqual(createHash('sha256').update(req.headers.get('authorization')??'').digest(),createHash('sha256').update('Bearer '+token).digest());
async function privateFetch(req){
 const path=new URL(req.url).pathname;
 if(goMode&&path==='/internal/staging/peer/status'){
  if(req.method!=='GET'||!authorized(req))return new Response(null,{status:401});
  return Response.json({...report,activities:[...applied.values()],objects:[...objects].map(([id,object])=>({id,object}))},{headers:{'cache-control':'no-store'}});
 }
 if(goMode&&path==='/internal/v2/events'&&req.method==='POST'&&authorized(req)){
  const reader=req.body?.getReader();if(!reader)return new Response(null,{status:400});const chunks=[];let size=0;
  for(;;){const r=await reader.read();if(r.done)break;size+=r.value.byteLength;if(size>2*1024*1024){await reader.cancel();return new Response(null,{status:413});}chunks.push(r.value);}
  const raw=Buffer.concat(chunks),response=await ingress.privateFetch(new Request(req.url,{method:'POST',headers:req.headers,body:raw}));
  if(response.status===202){
   const event=JSON.parse(raw.toString()),ack=await response.clone().json();
   const lose=!lostGoAck&&event.type==='content.published';if(lose)lostGoAck=true;
   report.cmsAttempts.push({eventId:event.event_id,bodySha256:createHash('sha256').update(raw).digest('hex'),originalStatus:response.status,deliveredStatus:lose?503:response.status,ack:ack.result});
   if(lose)return new Response('synthetic lost CMS acknowledgment',{status:503});
  }
  return response;
 }
 return ingress.privateFetch(req);
}
const servers=[serve({hostname:settings.host,port:settings.port,fetch:async req=>{
 if(draining)return new Response(null,{status:503});
 try{return new URL(req.url).pathname.startsWith('/_staging/')?await peer(req):await ingress.publicFetch(req);}
 catch{return new Response(null,{status:503});}
}}),serve({hostname:settings.host,port:settings.internalPort,fetch:privateFetch})];
const stop=async()=>{if(draining)return;draining=true;goController.abort();const clear=shutdownDeadline(settings.shutdownGraceMs);try{await Promise.all(servers.map(s=>new Promise((resolve,reject)=>s.close(e=>e?reject(e):resolve()))));await app.close();}finally{clear();}};
process.once('SIGTERM',()=>void stop());process.once('SIGINT',()=>void stop());
async function until(predicate,label,ms=120000){const deadline=Date.now()+ms;while(Date.now()<deadline){if(draining)throw Error('draining');if(await predicate())return;await new Promise(r=>setTimeout(r,1000));}throw Error('timeout: '+label);}
async function check(name,fn){await fn();report.checks.push(name);console.log(JSON.stringify({event:'staging_check_passed',name}));}
const follow={'@context':context,id:peerActor+'/follow',type:'Follow',actor:peerActor,object:actor};
let followed=false;
async function send(value,options){return http(signRfc(actor+'/inbox',value,pair.privateKey,peerKeyId,options));}
async function undo(){const value={'@context':context,id:peerActor+'/undo',type:'Undo',actor:peerActor,object:follow};return send(value);}
async function pending(){const r=await http(privateBase+'/internal/v2/status',{headers:authHeaders});assert.equal(r.status,200);return r.json();}
async function idle(){await until(async()=>{const s=await pending();return s.relay_recent&&s.transport.every(x=>x.dead||Number(x.count)===0)&&s.intents.every(x=>!['pending','leased'].includes(x.state)||Number(x.count)===0);},'queue drains');}
async function publish(value){const r=await http(privateBase+'/internal/v2/events',{method:'POST',headers:authHeaders,body:JSON.stringify(value)});assert.equal(r.status,202);return r.json();}
async function run(){
 try{
  await until(async()=>{try{const r=await http(origin+'/_staging/report');return r.status===200&&(await r.json()).runId===runId;}catch{return false;}},'new public deployment');
  await until(async()=>{try{return (await pending()).relay_recent;}catch{return false;}},'worker');report.state='running';
  await check('public HTTPS discovery, canonical proxy origin and private publishing boundary',async()=>{
   for(const path of ['/health/ready','/.well-known/webfinger?resource='+encodeURIComponent('acct:'+new URL(actor).pathname.split('/').at(-1)+'@'+new URL(actor).host),new URL(actor).pathname,new URL(actor).pathname+'/outbox']){
    const r=await http(origin+path,{headers:{accept:'application/activity+json','x-forwarded-host':'spoof.invalid','x-forwarded-proto':'http'}});assert.equal(r.status,200);
    if(path===new URL(actor).pathname)assert.equal((await r.json()).id,actor);
   }
   assert.equal((await http(origin+'/internal/v2/events',{method:'POST',headers:authHeaders,body:'{}'})).status,404);
   assert.equal((await http(privateBase+'/internal/v2/events',{method:'POST',headers:{'content-type':'application/json'},body:'{}'})).status,401);
  });
  await check('RFC9421 negative time, coverage, digest and actor ownership through public proxy',async()=>{
   for(const options of [{created:Math.floor(Date.now()/1000)-7200},{created:Math.floor(Date.now()/1000)+7200},{components:['@method','content-digest']},{digest:'sha-256=:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=:'}]){
    const r=await send({...follow,id:peerActor+'/bad/'+randomUUID()},options);assert.ok([400,401,403].includes(r.status));
   }
   assert.ok([400,401,403].includes((await send({...follow,id:peerActor+'/wrong-owner',actor:peerActor+'/other'})).status));
  });
  await check('signed Follow and independently verified outbound Accept; duplicate Follow is harmless',async()=>{
   assert.equal((await send(follow)).status,202);followed=true;
   await until(async()=>[...applied.values()].some(x=>x.type==='Accept'&&x.object.id===follow.id),'Accept');
   assert.equal((await send(follow)).status,202);await idle();assert.equal([...applied.values()].filter(x=>x.type==='Accept').length,1);
  });
  if(goMode){
   report.state='running-go';
   const {runGoProbe}=await import('./go-runner.fixture.mjs');
   await runGoProbe({report,privateBase,origin,actor,pending,idle,applied,objects,check,http,signal:goController.signal});
   await check('Go staging synthetic Follow is undone after verified delivery',async()=>{assert.equal((await undo()).status,202);await idle();const r=await http(actor+'/followers',{headers:{accept:'application/activity+json'}});assert.equal((await r.json()).totalItems,0);followed=false;});
   report.state='passed';report.finishedAt=new Date().toISOString();console.log(JSON.stringify({event:'staging_go_lifecycle_passed',checks:report.checks.length,runId}));return;
  }
  const content=randomUUID(),time=new Date().toISOString();
  const first={schema_version:2,publication_id:process.env.PUBLICATION_ID,event_id:randomUUID(),content_id:content,object_instance:randomUUID(),delivery_policy:'live',source:{id:process.env.PUBLISHER_ID,epoch:'1'},revision:'1',occurred_at:time,type:'content.published',document:{visibility:'public',title:'Disposable cloud staging article',html:'<p>Synthetic staging test only.</p>',url:origin+'/_staging/article/'+content,published_at:time}};
  let object;
  await check('durable Create retry after both formats receive 503 preserves payload and activity identity',async()=>{
   object=(await publish(first)).object_id;assert.equal((await publish(first)).result,'duplicate');
   await until(async()=>[...applied.values()].some(x=>x.type==='Create'&&x.object.id===object),'Create');
   await until(async()=>{const s=await pending();return s.transport.some(x=>Number(x.attempts_max)>=1);},'persisted durable retry');report.durableRetryObserved=true;await idle();
   const sent=report.attempts.filter(x=>x.type==='Create');assert.ok(sent.length>=3);assert.equal(new Set(sent.map(x=>x.id)).size,1);assert.equal(new Set(sent.map(x=>x.bodySha256)).size,1);assert.ok(report.duplicates.includes(sent[0].id));
   assert.equal((await http(object,{headers:{accept:'application/activity+json'}})).status,200);
  });
  await check('equal-time Update preserves object ID and advances public updated clock',async()=>{
   const before=objects.get(object);await publish({...first,event_id:randomUUID(),revision:'2',type:'content.updated',document:{...first.document,title:'Disposable updated staging article'}});
   await until(async()=>objects.get(object)?.name==='Disposable updated staging article','Update');assert.ok(Date.parse(objects.get(object).updated)>Date.parse(before.updated??before.published));
  });
  const {document,...base}=first;
  await check('withdrawal delivers signed Delete and public 410 Tombstone',async()=>{
   await publish({...base,event_id:randomUUID(),revision:'3',type:'content.withdrawn'});await until(async()=>objects.get(object)===null,'Delete');
   const r=await http(object,{headers:{accept:'application/activity+json'}});assert.equal(r.status,410);assert.equal((await r.json()).type,'Tombstone');
  });
  await check('republish creates a new object instance and preserves retired tombstone',async()=>{
   const republish={...first,event_id:randomUUID(),object_instance:randomUUID(),revision:'4'};
   const next=(await publish(republish)).object_id;assert.notEqual(next,object);await until(async()=>objects.has(next),'republish');
   assert.equal((await http(object,{headers:{accept:'application/activity+json'}})).status,410);
   await publish({...base,event_id:randomUUID(),object_instance:republish.object_instance,revision:'5',type:'content.deleted'});await until(async()=>objects.get(next)===null,'final deletion');
  });
  await check('legacy signed duplicate Follow and modern duplicate Undo remain compatible',async()=>{
   assert.equal((await http(legacy(follow))).status,202);await idle();assert.equal([...applied.values()].filter(x=>x.type==='Accept').length,1);
   assert.equal((await undo()).status,202);assert.equal((await undo()).status,202);await idle();
   const r=await http(actor+'/followers',{headers:{accept:'application/activity+json'}});assert.equal(r.status,200);assert.equal((await r.json()).totalItems,0);followed=false;
  });
  report.state='passed';report.finishedAt=new Date().toISOString();console.log(JSON.stringify({event:'staging_lifecycle_passed',checks:report.checks.length,runId}));
 }catch(e){report.state='failed';report.failure=e instanceof assert.AssertionError?'assertion_failed':(String(e.message).startsWith('timeout: ')||String(e.message).startsWith('go_probe_failure: '))?e.message:'request_or_runtime_error';report.finishedAt=new Date().toISOString();console.error(JSON.stringify({event:'staging_lifecycle_failed',completedChecks:report.checks.length,failure:report.failure,runId}));}
 finally{if(followed)try{await undo();}catch{}}
}
void run();
