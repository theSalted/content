import {test} from 'node:test';
import assert from 'node:assert/strict';
import {serve} from '@hono/node-server';
import {splitIngress} from '../src/ingress.ts';
import {processSettings} from '../src/process-settings.ts';
test('public and private ingress boundaries apply before dispatch, independent of forwarded headers',async()=>{
 let calls=0;
 const ingress=splitIngress(async()=>{calls++;return new Response('accepted');},true);
 for(const path of ['/internal/v2/events','/internal/v2/status','/internal/unknown']){
  const r=await ingress.publicFetch(new Request('http://public.example'+path,{headers:{'x-forwarded-host':'private.example','x-forwarded-port':'3001'}}));
  assert.equal(r.status,404);assert.equal(r.headers.get('cache-control'),'no-store');
 }
 assert.equal(calls,0);
 assert.equal((await ingress.privateFetch(new Request('http://private.example/users/editor'))).status,404);
 for(const path of ['/health/live','/health/ready','/internal/v2/events'])assert.equal((await ingress.privateFetch(new Request('http://private.example'+path))).status,200);
 assert.equal((await ingress.publicFetch(new Request('http://public.example/inbox'))).status,200);
 assert.throws(()=>processSettings({PORT:'3000',INTERNAL_PORT:'3000'}));
 assert.throws(()=>processSettings({INTERNAL_PORT:'0'}));
 assert.equal(processSettings({INTERNAL_PORT:'3001'}).internalPort,3001);
});
test('two actual HTTP listeners isolate public publishing and preserve health',async t=>{
 const ingress=splitIngress(request=>Response.json({path:new URL(request.url).pathname}),true);
 const listen=async(fetch:(r:Request)=>Response|Promise<Response>)=>{
  let server:ReturnType<typeof serve>;
  const base=await new Promise<string>(resolve=>{server=serve({hostname:'127.0.0.1',port:0,fetch},info=>resolve('http://127.0.0.1:'+info.port));});
  t.after(()=>new Promise<void>((resolve,reject)=>server.close(err=>err?reject(err):resolve())));return base;
 };
 const pub=await listen(ingress.publicFetch),privateBase=await listen(ingress.privateFetch);
 assert.equal((await fetch(pub+'/internal/v2/events',{method:'POST'})).status,404);
 assert.equal((await fetch(privateBase+'/internal/v2/events',{method:'POST'})).status,200);
 assert.equal((await fetch(pub+'/health/ready')).status,200);
 assert.equal((await fetch(privateBase+'/inbox',{method:'POST'})).status,404);
});
