// Disposable staging child-process adapter, excluded from normal runtime.
import {spawn} from 'node:child_process';
import assert from 'node:assert/strict';
import {mkdtemp,writeFile,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {rootCertificates} from 'node:tls';

export function redactedGoFailure(output){
 try{const value=JSON.parse(output);if(value.status==='failed'&&typeof value.failure==='string'&&/^[a-z0-9_]{1,80}$/.test(value.failure))return value.failure;}catch{}
 return 'nonzero_exit';
}
export function goProbeEnvironment(input,{privateBase,origin,runId}){
 const mapping={CONTENT_STAGING_CMS_API_DATABASE_URL:'CONTENT_DATABASE_URL',CONTENT_STAGING_CMS_DELIVERY_DATABASE_URL:'CONTENT_DELIVERY_DATABASE_URL',CONTENT_STAGING_CMS_SCHEMA:'CONTENT_SCHEMA',CONTENT_STAGING_CMS_INSTALLATION_ID:'CONTENT_INSTALLATION_ID'};
 const env={PATH:input.PATH,GOMEMLIMIT:'48MiB',GOGC:'50',GOMAXPROCS:'1'};
 for(const [name,target] of Object.entries(mapping)){assert.ok(input[name],name+' is required for the Go probe');env[target]=input[name];}
 if(input.CONTENT_STAGING_DATABASE_SSL_CA)env.CONTENT_STAGING_DATABASE_SSL_CA=input.CONTENT_STAGING_DATABASE_SSL_CA;
 Object.assign(env,{CONTENT_PUBLICATION_ID:input.PUBLICATION_ID,CONTENT_PUBLISHING_SOURCE:input.PUBLISHER_ID,
  CONTENT_PUBLISHING_DESTINATION:'activitypub',CONTENT_PUBLISHING_ENROLL_NEW:'true',CONTENT_PUBLISHING_DELIVERY_ENABLED:'true',
  CONTENT_PUBLISHING_ENDPOINT:privateBase+'/internal/v2/events',CONTENT_PUBLISHING_TOKEN:input.PUBLISHER_TOKEN,
  CONTENT_SITE_URL:origin,CONTENT_STAGING_PEER_STATUS_URL:privateBase+'/internal/staging/peer/status',CONTENT_STAGING_RUN_ID:runId});
 return env;
}
export async function runGoProbe({report,privateBase,origin,actor,pending,idle,applied,objects,check,http,signal}){
 const env=goProbeEnvironment(process.env,{privateBase,origin,runId:report.runId});
 // Slim Node images may have no OS CA store. Go uses system roots, whereas
 // Node uses its bundled Mozilla roots. Supply those same public trust roots
 // only to this child; never disable verification or mix in database-only CAs.
 const trustDirectory=await mkdtemp(join(tmpdir(),'content-go-public-roots-'));
 env.SSL_CERT_FILE=join(trustDirectory,'ca.pem');
 await writeFile(env.SSL_CERT_FILE,rootCertificates.join('\n')+'\n',{mode:0o600});
 // Explicit allowlist excludes AP DATABASE_URL, AP identity access and every
 // migration/owner setting. No shell, credential argument or raw child output.
 report.producer='actual-go-cms-outbox';let finished=false;
 const observe=async()=>{while(!finished&&!signal.aborted){try{const s=await pending();if(s.transport.some(x=>Number(x.attempts_max)>=1))report.durableRetryObserved=true;}catch{}await new Promise(r=>setTimeout(r,100));}};
 const monitor=observe();
 try{
  const result=await new Promise((resolve,reject)=>{
   const child=spawn('/app/staging-go-probe',[],{env,stdio:['ignore','pipe','pipe']}),out=[];
   let size=0,reason;
   const terminate=code=>{reason??=code;child.kill('SIGTERM');const force=setTimeout(()=>child.kill('SIGKILL'),5000);force.unref();};
   const timeout=setTimeout(()=>terminate('timeout'),180000);
   const abort=()=>terminate('aborted');signal.addEventListener('abort',abort,{once:true});
   child.stdout.on('data',chunk=>{size+=chunk.length;if(size>65536)terminate('oversized_output');else out.push(chunk);});child.stderr.resume();
   child.on('error',()=>{reason='unavailable';});
   child.on('close',code=>{
    clearTimeout(timeout);signal.removeEventListener('abort',abort);
    if(reason||code!==0){reject(Error('go_probe_failure: '+(reason??redactedGoFailure(Buffer.concat(out).toString('utf8')))));return;}
    try{const value=JSON.parse(Buffer.concat(out).toString('utf8'));assert.equal(value.status,'passed');assert.ok(Array.isArray(value.checks)&&value.checks.length>0&&value.checks.length<=40);assert.ok(value.checks.every(x=>typeof x==='string'&&x.length<=300));assert.match(value.content_id,/^[a-f0-9-]{36}$/);
     resolve({status:'passed',checks:value.checks,content_id:value.content_id});
    }catch{reject(Error('go_probe_failure: invalid_report'));}
   });
  });
  report.goProbe=result;
  await check('actual Go CMS immutable outbox, mapper and HTTP sender completed',async()=>assert.equal(result.status,'passed'));
  await idle();
  await check('Go outbox retries lost acknowledgment using identical persisted event bytes',async()=>{
   const lost=report.cmsAttempts.find(x=>x.originalStatus===202&&x.deliveredStatus===503);assert.ok(lost);
   const retry=report.cmsAttempts.filter(x=>x.eventId===lost.eventId);assert.ok(retry.length>=2);
   assert.equal(new Set(retry.map(x=>x.bodySha256)).size,1);assert.ok(retry.some(x=>x.deliveredStatus===202&&x.ack==='duplicate'));
  });
  await check('actual Go lifecycle reaches signed public peer in order with durable AP retry',async()=>{
   const activity=[...applied.values()];assert.deepEqual(activity.map(x=>x.type),['Accept','Create','Update','Delete','Create','Delete']);
   const [first,update,deleted,second,finalDelete]=activity.slice(1);assert.equal(first.object.id,update.object.id);assert.equal(deleted.object,first.object.id);assert.notEqual(second.object.id,first.object.id);assert.equal(finalDelete.object,second.object.id);
   assert.ok(Date.parse(update.object.updated)>Date.parse(first.object.updated??first.object.published));
   assert.equal(report.durableRetryObserved,true);
   const attempts=report.attempts.filter(x=>x.id===first.id);assert.ok(attempts.length>=3);assert.equal(new Set(attempts.map(x=>x.bodySha256)).size,1);
   for(const [id,value] of objects){assert.equal(value,null);const response=await http(id,{headers:{accept:'application/activity+json'}});assert.equal(response.status,410);assert.equal((await response.json()).type,'Tombstone');}
   assert.equal(objects.size,2);
  });
 }finally{finished=true;await monitor;await rm(trustDirectory,{recursive:true,force:true});}
}
