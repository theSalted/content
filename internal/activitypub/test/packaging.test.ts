import {test} from 'node:test';
import assert from 'node:assert/strict';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {randomUUID} from 'node:crypto';
import {connect,forPublication} from '../src/db.ts';
import {fingerprint} from '../src/identity.ts';
import {importExistingIdentity} from '../src/import-identity.ts';
import {readConfig} from '../src/config.ts';
import {published} from './helpers.ts';
const exec=promisify(execFile);
const image='content-ap-prep:local',migrator='content-ap-migrator-prep:local';
const docker=async(args:string[])=>(await exec('docker',args,{timeout:120000,maxBuffer:1024*1024})).stdout.trim();
async function eventually(check:()=>Promise<boolean>,name:string){for(let i=0;i<60;i++){if(await check())return;await new Promise(r=>setTimeout(r,250));}throw Error('Timed out: '+name);}
test('packaged API/worker and separate migrator on disposable PostgreSQL',{skip:process.env.CONTENT_ACTIVITYPUB_PACKAGING_TEST!=='1',timeout:120000},async t=>{
 const network='content-ap-package-'+randomUUID().slice(0,8),containers:string[]=[];
 let admin:ReturnType<typeof connect>|undefined;
 t.after(async()=>{await admin?.close();for(const id of containers.reverse()){
  await docker(['stop','--time','3',id]).catch(()=>{});await docker(['rm','--volumes',id]).catch(()=>{});
 }await docker(['network','rm',network]);});
 await docker(['network','create','--internal','--label','content.task=disposable-ap-packaging',network]);
 const run=async(args:string[])=>{const id=await docker(['run','--detach','--pull=never','--network',network,'--label','content.task=disposable-ap-packaging',...args]);containers.push(id);return id;};
 const address=async(id:string)=>{const ip=await docker(['inspect','--format','{{(index .NetworkSettings.Networks "'+network+'").IPAddress}}',id]);assert.match(ip,/^(10\.|172\.|192\.168\.)/);return 'http://'+ip+':3000';};
 const request=async(base:string,path='/health/ready',options:RequestInit={})=>fetch(base+path,{...options,signal:AbortSignal.timeout(4000)});
 const healthy=async(base:string)=>eventually(async()=>{try{return (await request(base)).status===200;}catch{return false;}},'health '+base);
 const stop=async(id:string)=>{await docker(['stop','--time','25',id]);assert.equal(await docker(['inspect','--format','{{.State.ExitCode}}',id]),'0');};
 const pg=await run(['--network-alias','pg','--memory','256m','--tmpfs','/var/lib/postgresql:rw,noexec,nosuid,size=256m','--env','POSTGRES_HOST_AUTH_METHOD=trust','--env','POSTGRES_DB=content_packaging_test','postgres:18-bookworm']);
 await eventually(async()=>{try{await docker(['exec',pg,'pg_isready','-h','127.0.0.1','-U','postgres','-d','content_packaging_test']);return true;}catch{return false;}},'PostgreSQL');
 const pgHost=new URL(await address(pg)).hostname;
 admin=connect('postgres://postgres@'+pgHost+':5432/content_packaging_test','package_ap','package-fixture');
 await admin.db.query('CREATE ROLE package_owner LOGIN NOSUPERUSER NOBYPASSRLS');
 await admin.db.query('CREATE ROLE package_runtime LOGIN NOSUPERUSER NOBYPASSRLS');
 await admin.db.query('CREATE SCHEMA package_ap AUTHORIZATION package_owner');
 const dbUrl='postgres://package_runtime@pg:5432/content_packaging_test';
 const common={ACTIVITYPUB_SCHEMA:'package_ap',CONTENT_INSTALLATION_ID:'package-fixture'};
 const envArgs=(env:Record<string,string>)=>Object.entries(env).flatMap(([k,v])=>['--env',k+'='+v]);
 const runMigration=async(user='package_owner')=>{
  const id=await run([...envArgs({...common,ACTIVITYPUB_MIGRATION_DATABASE_URL:'postgres://'+user+'@pg:5432/content_packaging_test'}),migrator]);
  return docker(['wait',id]);
 };
 await t.test('operator image migrates idempotently; runtime image omits operator/test material',async()=>{
  assert.equal(await runMigration(),'0');assert.equal(await runMigration(),'0');
  const id=await run([image,'node','-e',"const fs=require('fs');if(process.getuid()===0)process.exit(2);for(const p of ['src/migrate.ts','src/import-identity.ts','migrations','test','node_modules/typescript'])if(fs.existsSync(p))process.exit(3)"]);
  assert.equal(await docker(['wait',id]),'0');
  assert.notEqual(await runMigration('package_runtime'),'0');
 });
 const limits=['--memory','256m','--cpus','1','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges'];
 await t.test('both disabled processes start without credentials, identity or database and drain',async()=>{
  for(const command of ['src/main.ts','src/worker-main.ts']){
   const id=await run([...limits,image,'node',command]),base=await address(id);await healthy(base);
   assert.deepEqual(await (await request(base)).json(),{status:'disabled'});
   assert.equal((await request(base,'/inbox',{method:'POST'})).status,command.includes('worker')?404:503);
   await stop(id);
  }
 });
 const env={...common,DATABASE_URL:dbUrl,DATABASE_POOL_SIZE:'5',PUBLICATION_ID:'package-fixture',ACTOR_ID:'https://packaging.example/actors/editor',ACTOR_KEY_ID:'https://packaging.example/actors/editor#main-key',ACTOR_NAME:'Synthetic editor',ACTOR_PROFILE_URL:'https://packaging.example/about',PUBLISHER_ID:'content',PUBLISHER_TOKEN:'synthetic-packaging-token-not-for-production',FEDERATION_ENABLED:'true',EXPECTED_PUBLIC_KEY_SHA256:'0'.repeat(64)};
 await admin.db.query('INSERT INTO {{schema}}.publication_roles VALUES($1,$2)',[env.PUBLICATION_ID,'package_runtime']);
 await admin.db.query('GRANT USAGE ON SCHEMA package_ap TO package_runtime');
 await admin.db.query('GRANT SELECT ON package_ap.identities,package_ap.installation,package_ap.publication_roles TO package_runtime');
 await admin.db.query('GRANT SELECT,INSERT ON package_ap.object_id_reservations TO package_runtime');
 // Derive names from this owned fixture schema; never grant on protected state.
 const tables=await admin.db.query<{tablename:string}>('SELECT tablename FROM pg_tables WHERE schemaname=$1',['package_ap']);
 for(const {tablename} of tables)if(!['identities','installation','publication_roles','schema_migrations','object_id_reservations'].includes(tablename)){
  assert.match(tablename,/^[a-z_]+$/);await admin.db.query('GRANT SELECT,INSERT,UPDATE,DELETE ON package_ap.'+tablename+' TO package_runtime');
 }
 await admin.db.query('GRANT USAGE ON ALL SEQUENCES IN SCHEMA package_ap TO package_runtime');
 await t.test('enabled startup refuses missing identity without generating one',async()=>{
  const id=await run([...limits,...envArgs(env),image]);assert.equal(await docker(['wait',id]),'1');
  assert.equal((await admin!.db.query<{n:number}>('SELECT count(*)::int AS n FROM {{schema}}.identities'))[0]!.n,0);
 });
 const keys=await crypto.subtle.generateKey({name:'RSASSA-PKCS1-v1_5',hash:'SHA-256',modulusLength:2048,publicExponent:new Uint8Array([1,0,1])},true,['sign','verify']);
 env.EXPECTED_PUBLIC_KEY_SHA256=await fingerprint(keys.publicKey);
 await importExistingIdentity(forPublication(admin.db,env.PUBLICATION_ID),readConfig(env),{actor_id:env.ACTOR_ID,key_id:env.ACTOR_KEY_ID,public_jwk:await crypto.subtle.exportKey('jwk',keys.publicKey),private_jwk:await crypto.subtle.exportKey('jwk',keys.privateKey)});
 const api=await run([...limits,...envArgs({...env,INTERNAL_PORT:'3001'}),image]),base=await address(api),privateBase=base.replace(':3000',':3001');await healthy(base);
 const samples:unknown[]=[];
 const measure=async(label:string,id:string)=>{
  const data=await docker(['exec',id,'node','-e',"const fs=require('fs');console.log(JSON.stringify({rssKb:Number(fs.readFileSync('/proc/1/status','utf8').match(/VmRSS:\\s+(\\d+)/)[1]),cgroupPeakBytes:Number(fs.readFileSync('/sys/fs/cgroup/memory.peak','utf8'))}))"]);
  samples.push({label,...JSON.parse(data)});
 };
 await t.test('API is ready before worker; immutable event/retry reaches persisted public object',async()=>{
  assert.equal((await request(base)).status,200);
  const e=published({publication_id:env.PUBLICATION_ID,source:{id:'content',epoch:'1'}});e.document.url='https://packaging.example/article';
  assert.equal((await request(base,'/internal/v2/events',{method:'POST'})).status,404);
  assert.equal((await request(privateBase,'/inbox',{method:'POST'})).status,404);
  const post=()=>request(privateBase,'/internal/v2/events',{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer '+env.PUBLISHER_TOKEN},body:JSON.stringify(e)});
  assert.equal((await post()).status,202);assert.equal((await (await post()).json()).result,'duplicate');
  const actor=await request(base,'/actors/editor',{headers:{accept:'application/activity+json'}});assert.equal((await actor.json()).id,env.ACTOR_ID);
  await measure('api-after-one-publish-and-retry',api);
 });
 const worker=await run([...limits,...envArgs(env),image,'node','src/worker-main.ts']),workerBase=await address(worker);await healthy(workerBase);
 await t.test('worker readiness tracks both loops; pools are bounded; SIGTERM exits cleanly',async()=>{
  await measure('worker-idle-after-startup',worker);
  const rows=await admin!.db.query<{n:number}>("SELECT count(*)::int AS n FROM pg_stat_activity WHERE usename='package_runtime'");
  const n=rows[0]!.n;assert.ok(n>0&&n<=10);console.log(JSON.stringify({packagingMeasurements:samples,runtimeConnections:n,configuredCombinedMax:10}));
  assert.equal((await request(workerBase,'/internal/v2/events',{method:'POST'})).status,404);
  await stop(worker);
 });
 await t.test('API liveness survives DB outage while readiness fails within HTTP budget',async()=>{
  await docker(['stop','--time','3',pg]);
  assert.equal((await request(base,'/health/live')).status,200);
  const started=Date.now();assert.equal((await request(base)).status,503);assert.ok(Date.now()-started<3500);
  assert.equal((await request(base,'/health/live')).status,200);await stop(api);
 });
});
