// Disposable Go-to-Node integration oracle. No production configuration accepted.
import {serve} from '@hono/node-server';
import {MemoryKvStore} from '@fedify/fedify';
import {fixture,CaptureQueue} from './helpers.ts';
import {makeFederation} from '../src/federation.ts';
import {makeApp} from '../src/app.ts';
if(process.env.CONTENT_ACTIVITYPUB_TEST_SERVER!=='1')throw new Error('Explicit synthetic fixture flag required');
const parsed=new URL(process.env.AP_TEST_ORIGIN??'https://federation.example');
const origin=parsed.origin;
if(parsed.protocol!=='https:'||parsed.href!==parsed.origin+'/')throw new Error('Synthetic origin must be an HTTPS origin');
const f=await fixture({publicationId:process.env.AP_TEST_PUBLICATION_ID??'publication-example',actorId:origin+'/actors/editor'});
f.config.publisherId='content';
// No document fetching or remote delivery. The server only persists synthetic
// events to an in-memory PostgreSQL engine for cross-runtime contract checks.
globalThis.fetch=async()=>{throw new Error('External access forbidden in synthetic contract server');};
const queue=new CaptureQueue();
const federation=makeFederation(f.state,new MemoryKvStore(),queue);
const app=makeApp(f.state,federation);
const server=serve({hostname:'127.0.0.1',port:0,fetch:async request=>{
 if(new URL(request.url).pathname==='/_test/state')return Response.json({
  objects:await f.db.query('SELECT content_id,object_id,instance_id,state,revision::text FROM {{schema}}.content_objects'),
  activities:await f.db.query('SELECT kind,public,payload FROM {{schema}}.activities ORDER BY sequence'),
  receipts:await f.db.query('SELECT event_id,result,revision::text FROM {{schema}}.publishing_events')});
 return app(request);
}},info=>console.log(JSON.stringify({url:`http://127.0.0.1:${info.port}`,publication_id:f.config.publicationId,actor_id:f.config.actorId})));
let stopping=false;
async function stop(){if(stopping)return;stopping=true;server.close(async()=>{await f.close();process.exit(0);});}
process.once('SIGTERM',()=>void stop());process.once('SIGINT',()=>void stop());
