import {test} from 'node:test';
import {randomUUID} from 'node:crypto';
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {createInterface} from 'node:readline';
import {once} from 'node:events';
import {fileURLToPath} from 'node:url';
import {readFile} from 'node:fs/promises';
test('synthetic HTTP oracle durably accepts v2 and isolates publisher scope',{skip:process.env.CONTENT_ACTIVITYPUB_HTTP_TEST!=='1',timeout:15000},async t=>{
 const child=spawn(process.execPath,[fileURLToPath(new URL('./contract-server.fixture.ts',import.meta.url))],{env:{CONTENT_ACTIVITYPUB_TEST_SERVER:'1'},stdio:['ignore','pipe','inherit']});
 const exited=once(child,'exit');t.after(async()=>{child.kill('SIGTERM');await exited;});
 const lines=createInterface({input:child.stdout!});const [line]=await once(lines,'line');lines.close();
 const {url}=JSON.parse(line);assert.match(url,/^http:\/\/127\.0\.0\.1:\d+$/);
 const event=JSON.parse(await readFile(new URL('../contracts/published.example.json',import.meta.url),'utf8'));event.source.id='content';
 const send=(body:unknown)=>fetch(url+'/internal/v2/events',{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer isolated-test-publisher-token-never-use-live'},body:JSON.stringify(body)});
 assert.equal((await send(event)).status,202);assert.equal((await (await send(event)).json()).result,'duplicate');
 assert.equal((await send({...event,publication_id:'forbidden'})).status,403);
 const state=await(await fetch(url+'/_test/state')).json();assert.equal(state.objects.length,1);assert.equal(state.activities.length,1);assert.equal(state.activities[0].kind,'Create');
 const cases=JSON.parse(await readFile(new URL('../contracts/fixtures/v2-cases.json',import.meta.url),'utf8'));
 let accepted=1;
 for(const c of cases.filter((c:{name:string})=>c.name.startsWith('title-'))){
  const e=structuredClone(c.event);e.event_id=randomUUID();e.content_id=randomUUID();e.object_instance=e.content_id;
  const response=await send(e);assert.equal(response.status,c.valid?202:422,c.name);
  if(c.valid){accepted++;const current=await(await fetch(url+'/_test/state')).json();
   assert.equal(current.activities.at(-1).payload.object.name,e.document.title,c.name+' exact title');}
 }
 const final=await(await fetch(url+'/_test/state')).json();assert.equal(final.activities.length,accepted);
});
