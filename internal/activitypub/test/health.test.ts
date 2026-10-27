import {test} from 'node:test';
import assert from 'node:assert/strict';
import {healthHandler,workerProgress} from '../src/health.ts';
import {connectionOptions} from '../src/db.ts';
import {processSettings} from '../src/process-settings.ts';
import {createModule} from '../src/module.ts';
const request=(path:string,method='GET')=>new Request('http://healthcheck.railway.app'+path,{method});
test('liveness is independent, readiness is bounded and never leaks error details',async()=>{
 let calls=0,release!:(value:boolean)=>void;
 const check=()=>{calls++;return new Promise<boolean>(resolve=>{release=resolve;});};
 const health=healthHandler(check,{timeoutMs:10});
 assert.equal((await health.fetch(request('/health/live')))?.status,200);assert.equal(calls,0);
 const results=await Promise.all(Array.from({length:5},()=>health.fetch(request('/health/ready'))));
 assert.equal(calls,1);assert.ok(results.every(x=>x?.status===503));
 assert.equal((await health.fetch(request('/health/ready')))?.status,503);assert.equal(calls,1);
 release(true);await new Promise(resolve=>setImmediate(resolve));
 const failed=healthHandler(async()=>{throw new Error('secret database URL');});
 assert.deepEqual(await (await failed.fetch(request('/health/ready')))!.json(),{status:'unavailable'});
});
test('disabled module is explicitly ready without DB/keys; other routes stay disabled',async()=>{
 const app=await createModule({});
 assert.deepEqual(await (await app.fetch(request('/health/ready'))).json(),{status:'disabled'});
 assert.equal((await app.fetch(request('/inbox','POST'))).status,503);
 await app.close();
});
test('health requests do not cache, HEAD is empty, draining fails readiness only',async()=>{
 const health=healthHandler(async()=>true);
 const r=await health.fetch(request('/health/ready','HEAD'));assert.equal(r?.status,200);assert.equal(await r?.text(),'');
 assert.equal(r?.headers.get('cache-control'),'no-store');
 assert.equal((await health.fetch(request('/health/ready','POST')))?.status,405);
 health.drain();assert.equal((await health.fetch(request('/health/ready')))?.status,503);
 assert.equal((await health.fetch(request('/health/live')))?.status,200);
 assert.equal(await health.fetch(request('/unrelated')),undefined);
});
test('worker requires its own fresh relay AND transport progress',()=>{
 let now=0;const p=workerProgress(()=>now,100);
 assert.equal(p.ready(),false);p.tick('relay');assert.equal(p.ready(),false);p.tick('transport');assert.equal(p.ready(),true);
 now=101;p.tick('relay');assert.equal(p.ready(),false);p.tick('transport');assert.equal(p.ready(),true);
});
test('connection caps/timeouts and custom CA validation remain bounded',()=>{
 for(const max of [0,1,21,Infinity,2.5])assert.throws(()=>connectionOptions(max));
 const runtime=connectionOptions(5,{sslCa:'synthetic-public-ca'});
 assert.equal(runtime.max,5);assert.equal(runtime.ssl?.rejectUnauthorized,true);
 assert.equal(runtime.connection.statement_timeout,15000);assert.equal(runtime.connection.lock_timeout,10000);
 const migration=connectionOptions(2,{profile:'migration'});assert.equal(migration.max,2);assert.equal(migration.connection.statement_timeout,60000);
 assert.throws(()=>processSettings({PORT:'garbage'}));assert.throws(()=>processSettings({SHUTDOWN_GRACE_MS:'0'}));
 assert.equal(processSettings({}).shutdownGraceMs,20000);
});
