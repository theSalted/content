import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {mkdtemp,writeFile,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {test} from 'node:test';
import assert from 'node:assert/strict';
import {randomUUID} from 'node:crypto';
import {connect} from '../src/db.ts';
import {published} from './helpers.ts';

const enabled=process.env.CONTENT_ACTIVITYPUB_MASTODON_TEST==='1';
const execute=promisify(execFile);
const image='ghcr.io/mastodon/mastodon@sha256:b009655daafa12a04f1e520221b7a6b84fd11cc576c17dd96d2a2c05b1ca7ebe';
const redisImage='redis@sha256:57e4255cc971b1c8d6f8a6ca283ac01bc3ecdd914273ef6bfaede87aaecd6541';
const nodeImage='node@sha256:a9f5f7c91a432850b2a8a7797adf5eadb6c733ceed61167806cee7ea7fbc29df';
async function docker(args:string[]){return (await execute('docker',args,{maxBuffer:8*1024*1024,timeout:120000})).stdout.trim();}
async function logs(id:string){const r=await execute('docker',['logs','--tail','35',id]);return r.stdout+r.stderr;}
async function eventually(predicate:()=>Promise<boolean>,label:string,timeout=30000){
 const until=Date.now()+timeout;while(Date.now()<until){if(await predicate())return;await new Promise(r=>setTimeout(r,250));}throw Error('Timed out: '+label);
}
type Snapshot={trace:{path:string;status:number;detail?:string}[];followers:number;queue:{queued:number};deliveries:{type:string;format:string;status:number;id:string;bodyHash:string;semanticHash:string}[];discovery:string[];incoming:string[];tunnels:string[]};

test('on-Follow history against official Mastodon 5.0.0-nightly.2026-10-22',{skip:!enabled,timeout:300000},async t=>{
 const network='content-masto-'+randomUUID().slice(0,8),containers:string[]=[];
 const directory=await mkdtemp(join(tmpdir(),'content-mastodon-'));let admin:ReturnType<typeof connect>|undefined;
 t.after(async()=>{
  await admin?.close();
  for(const container of containers.reverse()){
   await docker(['stop','--time','3',container]).catch(()=>{});
   await docker(['rm','--volumes',container]).catch(()=>{});
  }
  await docker(['network','rm',network]);await rm(directory,{recursive:true,force:true});
 });
 const run=async(args:string[])=>{const id=await docker(['run','--detach','--pull=never','--label','content.task=disposable-mastodon-tests','--network',network,...args]);containers.push(id);return id;};
 const address=async(id:string)=>{
  const ip=await docker(['inspect','--format','{{(index .NetworkSettings.Networks "'+network+'").IPAddress}}',id]);assert.match(ip,/^(172\.|192\.168\.|10\.)/);return ip;
 };
 await docker(['network','create','--internal','--label','content.task=disposable-mastodon-tests',network]);
 const pg=await run(['--network-alias','pg','--memory','512m','--tmpfs','/var/lib/postgresql:rw,noexec,nosuid,size=256m','--env','POSTGRES_HOST_AUTH_METHOD=trust','--env','POSTGRES_DB=content_mastodon_test','postgres:18-bookworm']);
 await run(['--network-alias','redis','--memory','128m','--tmpfs','/data:rw,noexec,nosuid,size=64m',redisImage,'redis-server','--save','','--appendonly','no']);
 await eventually(async()=>{try{await docker(['exec',pg,'pg_isready','-U','postgres','-d','content_mastodon_test']);return true;}catch{return false;}},'disposable PostgreSQL');
 admin=connect('postgres://postgres@'+await address(pg)+':5432/content_mastodon_test','fixture_inspection');
 await execute('openssl',['req','-x509','-newkey','rsa:2048','-nodes','-keyout',join(directory,'key.pem'),'-out',join(directory,'cert.pem'),'-days','1','-subj','/CN=federation.example','-addext','subjectAltName=DNS:federation.example,DNS:mastodon.example','-addext','basicConstraints=critical,CA:TRUE']);
 const token=randomUUID();
 const endpoint=await run(['--network-alias','content-fixture','--memory','768m','--cpus','2','--cap-drop','ALL','--security-opt','no-new-privileges',
  '--mount','type=bind,source='+fileURLToPath(new URL('..',import.meta.url))+',target=/module,readonly',
  '--mount','type=bind,source='+fileURLToPath(new URL('../node_modules',import.meta.url))+',target=/module/node_modules,readonly',
  '--mount','type=bind,source='+directory+',target=/fixture,readonly',
  '--workdir','/module','--env','CONTENT_MASTODON_FIXTURE=synthetic-only','--env','FIXTURE_CONTROL_TOKEN='+token,
  nodeImage,'node','test/mastodon/content-history-fixture.ts']);
 const controlURL='http://'+await address(endpoint)+':8081';
 const control=async<T=unknown>(action:string,extra:Record<string,unknown>={}):Promise<T>=>{
  const response=await fetch(controlURL,{method:'POST',headers:{authorization:'Bearer '+token,'content-type':'application/json'},body:JSON.stringify({action,...extra}),signal:AbortSignal.timeout(30000)});
  assert.equal(response.status,200,'fixture command '+action);return (await response.json()).result as T;
 };
 try{await eventually(async()=>{try{await control('snapshot');return true;}catch{return false;}},'containerized content fixture');}
 catch(error){console.error(await logs(endpoint));throw error;}
 const env={RAILS_ENV:'production',LOCAL_DOMAIN:'mastodon.example',LOCAL_HTTPS:'true',DB_HOST:'pg',DB_PORT:'5432',DB_USER:'postgres',DB_NAME:'content_mastodon_test',DB_PASS:'',REDIS_HOST:'redis',REDIS_PORT:'6379',SECRET_KEY_BASE:randomUUID().repeat(4),ACTIVE_RECORD_ENCRYPTION_PRIMARY_KEY:randomUUID(),ACTIVE_RECORD_ENCRYPTION_DETERMINISTIC_KEY:randomUUID(),ACTIVE_RECORD_ENCRYPTION_KEY_DERIVATION_SALT:randomUUID(),RAILS_LOG_TO_STDOUT:'true',RAILS_LOG_LEVEL:'warn',RAILS_SERVE_STATIC_FILES:'true',WEB_CONCURRENCY:'0',MAX_THREADS:'2',DB_POOL:'5',ES_ENABLED:'false',S3_ENABLED:'false',DISABLE_RATE_LIMITS:'true',SMTP_SERVER:'127.0.0.1',SMTP_PORT:'9',http_proxy:'http://content-fixture:8080',SSL_CERT_FILE:'/fixture/cert.pem'};
 await writeFile(join(directory,'env'),Object.entries(env).map(([k,v])=>k+'='+v).join('\n')+'\n',{mode:0o600});
 const common=['--env-file',join(directory,'env'),'--mount','type=bind,source='+join(directory,'cert.pem')+',target=/fixture/cert.pem,readonly','--tmpfs','/mastodon/public/system:rw,nosuid,size=64m,mode=1777'];
 await t.test('container-to-container HTTPS verifies fixture certificate and allowlisted proxy',async()=>{
  const result=await docker(['run','--rm','--pull=never','--network',network,...common,image,'curl','--silent','--show-error','--fail','--max-time','10','--proxy',env.http_proxy,'--cacert','/fixture/cert.pem','https://federation.example/health/live']);
  assert.equal(JSON.parse(result).status,'up');
  await assert.rejects(docker(['run','--rm','--pull=never','--network',network,...common,image,'curl','--silent','--fail','--max-time','5','--proxy',env.http_proxy,'--cacert','/etc/ssl/certs/ca-certificates.crt','https://federation.example/health/live']),'Untrusted fixture certificate must fail');
  await assert.rejects(docker(['run','--rm','--pull=never','--network',network,...common,image,'curl','--silent','--fail','--max-time','5','--proxy',env.http_proxy,'https://unlisted.invalid/']),'Proxy must reject an unlisted origin before egress');
 });
 const web=await run(['--network-alias','mastodon-web','--memory','1536m','--cpus','2',...common,'--entrypoint','sh',image,'-c','bundle exec rails db:prepare && exec bundle exec puma -C config/puma.rb']);
 const webUrl='http://'+await address(web)+':3000';
 try{await eventually(async()=>{
  const status=await docker(['inspect','--format','{{.State.Status}}',web]);if(status==='exited')throw Error('Mastodon exited during boot');
  try{return (await fetch(webUrl+'/health',{headers:{host:'mastodon.example','x-forwarded-proto':'https'},signal:AbortSignal.timeout(2000)})).ok;}catch{return false;}
 },'Mastodon Puma health',90000);}catch(error){console.error(await logs(web));throw error;}
 const worker=await run(['--memory','1024m','--cpus','1',...common,image,'bundle','exec','sidekiq','-c','2']);
 const rails=(code:string)=>docker(['exec',web,'bundle','exec','rails','runner',code]);
 const dbCount=async(table:string,where:string,params:unknown[]=[])=>Number((await admin!.db.query<{n:number}>(`SELECT count(*) AS n FROM public.${table} WHERE ${where}`,params))[0]!.n);
 const snapshot=()=>control<Snapshot>('snapshot');
 // Seed silent history before Mastodon knows or follows this account.
 for(let n=0;n<21;n++){
  const e=published({publication_id:'mastodon-peer',source:{id:'content',epoch:'1'}});
  const date=new Date(Date.UTC(2020,0,n+1)).toISOString();
  await control('archive',{event:{...e,schema_version:1,type:'content.archived',delivery_policy:'archive',source_state:'published',occurred_at:date,document:{...e.document,published_at:date,title:n===0?'Note-shaped archive control':'Archived article '+n}}});
 }
 assert.equal(await control('relay'),false);
 let followed=false,created=false,deleted=false,undone=false;
 try{
 await t.test('Mastodon discovers WebFinger/actor, signs Follow, accepts negotiated signed Accept',async()=>{
  await rails("ActionMailer::Base.perform_deliveries=false; Rails.configuration.x.email_domains_allowlist=['example.invalid']; u=User.new(email: 'alice@example.invalid', password: SecureRandom.hex(24), approved: true, agreement: true, account: Account.new(username: 'alice', display_name: 'Synthetic Alice')); u.skip_confirmation!; u.save!; u.update!(approved: true); raise 'Synthetic user is not functional' unless u.reload.functional?; a=u.account; b=ActivityPub::FetchRemoteActorService.new.call('https://federation.example/users/editor', suppress_errors: false); raise 'Actor discovery failed' if b.nil?; FollowService.new.call(a,b)");
  await eventually(async()=>{const state=await snapshot();if(state.trace.some(r=>r.path.endsWith('/inbox')&&r.status>=400))throw Error('Follow rejected: '+JSON.stringify(state.trace));return state.queue.queued>0;},'signed Mastodon Follow');await control('step');assert.equal((await snapshot()).followers,1);
  assert.equal(await control('relay'),true);await control('step');await eventually(async()=>await dbCount('follows','true')===1,'Mastodon accepted Follow');
  const state=await snapshot();assert.ok(state.incoming.includes('Follow'));assert.ok(state.discovery.some(p=>p.startsWith('/.well-known/webfinger?')));
  assert.ok(state.deliveries.some(d=>d.type==='Accept'&&['rfc9421','cavage'].includes(d.format)&&d.status===202),JSON.stringify(state.deliveries));
  assert.equal(await control('replay-follow'),202);await control('step');assert.equal(await control('relay'),false);assert.equal((await snapshot()).followers,1);followed=true;
 });
 assert.ok(followed,'Follow prerequisite failed');
 await t.test('signed actor Update refreshes hidden collections and exposes only locally known followers',async()=>{
  const account=(await admin!.db.query<{id:string;hide_collections:boolean}>("SELECT id::text AS id,hide_collections FROM public.accounts WHERE username='editor' AND domain='federation.example'"))[0]!;
  assert.equal(account.hide_collections,true);assert.equal(await dbCount('follows','true'),1);
  const before=await control<{status:number;body:unknown[]}>('peer-account-followers',{id:account.id});assert.equal(before.status,200);assert.equal(before.body.length,0);
  await control('profile-public-update');await control('step');
  await eventually(async()=>!(await admin!.db.query<{hide_collections:boolean}>('SELECT hide_collections FROM public.accounts WHERE id=$1',[account.id]))[0]!.hide_collections,'actor Update collection privacy refresh');
  const after=await control<{status:number;body:unknown[]}>('peer-account-followers',{id:account.id});assert.equal(after.status,200);assert.equal(after.body.length,1);assert.equal(await dbCount('follows','true'),1);
  console.log(JSON.stringify({profileUpdateRefreshedHideCollections:true,followersListBefore:0,followersListAfter:1,followRowsUnchanged:1}));
 });
 await t.test('new accepted Follow delivers original archive to profile; retry, Undo and refollow remain bounded',async()=>{
  // Only Accept has been sent. History jobs are durable, but cannot overtake it.
  assert.equal(await dbCount('statuses','true'),0);
  await control('history-ready');await control('fail-create');assert.equal(await control('relay'),true);
  assert.equal(await dbCount('statuses','true'),0);await control('allow-create');
  await control('history-ready');await control('relay');
  await eventually(async()=>await dbCount('statuses','true')===1,'first historical Create');
  const first=(await snapshot()).deliveries.filter(x=>x.type==='Create');assert.equal(new Set(first.map(x=>x.id)).size,1);assert.equal(new Set(first.map(x=>x.semanticHash)).size,1);
  // Undo cancels unsent jobs. Fresh Follow resumes those only; one delivered
  // history item must never repeat. No test-only writes touch Mastodon state.
  await rails("UnfollowService.new.call(Account.find_local!('alice'),Account.find_by!(username:'editor',domain:'federation.example'))");
  await eventually(async()=>(await snapshot()).queue.queued>0,'Undo before remaining history');await control('step');assert.equal((await snapshot()).followers,0);
  assert.deepEqual((await control<any[]>('history-state')).sort((a,b)=>a.state.localeCompare(b.state)),[{state:'dead',n:20},{state:'enqueued',n:1}]);
  await control('history-ready');assert.equal(await control('relay'),false);
  await rails("FollowService.new.call(Account.find_local!('alice'),Account.find_by!(username:'editor',domain:'federation.example'))");
  await eventually(async()=>(await snapshot()).queue.queued>0,'fresh Follow');await control('step');await control('relay');await control('step');
  await eventually(async()=>await dbCount('follows','true')===1,'new Accept');
  // Exercise configured real elapsed spacing (no advancing queue deadlines).
  for(let n=1;n<21;n++){
   await eventually(async()=>await control<boolean>('relay'),'history job '+n);
   await eventually(async()=>await dbCount('statuses','true')===n+1,'historical status '+n);
  }
  const state=await snapshot(),creates=state.deliveries.filter(x=>x.type==='Create'&&x.status===202);
  assert.equal(creates.length,21);assert.equal(new Set(creates.map(x=>x.id)).size,21);
  assert.ok(!state.discovery.some(p=>p.includes('/outbox?')),'No manual archive crawl should be needed');
  const dates=await admin!.db.query<{created_at:Date}>('SELECT created_at FROM public.statuses ORDER BY created_at');
  assert.equal(dates.length,21);assert.ok(dates.every(x=>x.created_at.getUTCFullYear()===2020));
  const account=(await admin!.db.query<{id:string}>("SELECT id::text AS id FROM public.accounts WHERE username='editor' AND domain='federation.example'"))[0]!;
  const api=await control<{status:number;body:unknown[]}>('peer-account-statuses',{id:account.id});assert.equal(api.status,200);assert.equal(api.body.length,21);
  console.log(await rails("a=Account.find_local!('alice'); b=Account.find_by!(username:'editor',domain:'federation.example'); puts 'FOLLOW_HISTORY_RESULT='+{profile_statuses:b.statuses.count,home_feed_entries:HomeFeed.new(a).get(40).count,accepted_follow:a.following?(b),original_years:b.statuses.pluck(:created_at).map(&:year).uniq}.to_json"));
  // Fully delivered history does not repeat on another Undo/Follow cycle.
  await rails("UnfollowService.new.call(Account.find_local!('alice'),Account.find_by!(username:'editor',domain:'federation.example'))");
  await eventually(async()=>(await snapshot()).queue.queued>0,'second Undo');await control('step');
  await rails("FollowService.new.call(Account.find_local!('alice'),Account.find_by!(username:'editor',domain:'federation.example'))");
  await eventually(async()=>(await snapshot()).queue.queued>0,'second fresh Follow');await control('step');await control('relay');await control('step');
  await eventually(async()=>await dbCount('follows','true')===1,'second Accept');
  await control('history-ready');assert.equal(await control('relay'),false);
  assert.equal((await snapshot()).deliveries.filter(x=>x.type==='Create'&&x.status===202).length,21);
 });

 const post=published({publication_id:'mastodon-peer',source:{id:'content',epoch:'1'}});let objectId='',statusId='';
 await t.test('Mastodon receives retried Create, duplicate delivery and Update as one status',async()=>{
  objectId=(await control<{object_id:string}>('publish',{event:post})).object_id;await control('fail-create');await control('relay');await control('step');
  assert.equal((await snapshot()).queue.queued,1);assert.ok((await snapshot()).deliveries.some(d=>d.type==='Create'&&d.status===503));await control('allow-create');await control('advance');await control('step');
  await eventually(async()=>await dbCount('statuses','uri=$1',[objectId])===1,'Mastodon Create');assert.equal(await control('replay-create'),202);
  const update={...post,event_id:randomUUID(),revision:'2',type:'content.updated',document:{...post.document,title:'Mastodon updated',html:'<p>Changed synthetic article body</p>'}};
  await control('publish',{event:update});await control('relay');await control('step');
  await eventually(async()=>{const rows=await admin!.db.query<{text:string}>('SELECT text FROM public.statuses WHERE uri=$1',[objectId]);return rows[0]?.text.includes('Mastodon updated')??false;},'Mastodon Update');
  assert.equal(await dbCount('statuses','uri=$1',[objectId]),1);
  const projection=(await admin!.db.query<{text:string}>('SELECT text FROM public.statuses WHERE uri=$1',[objectId]))[0]!.text;
  assert.ok(projection.includes(post.document.url));assert.ok(!projection.includes('Changed synthetic article body'));
  const allCreates=(await snapshot()).deliveries.filter(d=>d.type==='Create');const creates=allCreates.filter(d=>d.id===allCreates.at(-1)!.id);
  assert.equal(new Set(creates.map(d=>d.id)).size,1);assert.equal(new Set(creates.map(d=>d.bodyHash)).size,1);
  statusId=(await admin!.db.query<{id:string}>('SELECT id::text AS id FROM public.statuses WHERE uri=$1',[objectId]))[0]!.id;
  const response=await control<{status:number;body:{content:string}}>('peer-status',{id:statusId});assert.equal(response.status,200);
  const rendered=response.body;assert.ok(rendered.content.includes('Mastodon updated'));assert.ok(rendered.content.includes(post.document.url));assert.ok(!rendered.content.includes('Changed synthetic article body'));
  const original=await control<{content:string;updated:string}>('object',{id:objectId});assert.equal(original.content,update.document.html);assert.notEqual(original.updated,post.occurred_at);
  console.log(JSON.stringify({articleProjection:projection,apiContent:rendered.content,publicUpdated:original.updated}));
  assert.ok((await snapshot()).deliveries.some(d=>d.type==='Create'&&['rfc9421','cavage'].includes(d.format)&&d.status===202));created=true;
 });
 assert.ok(created,'Create/Update prerequisite failed');
 await t.test('withdrawal removes the Mastodon status and preserves the original tombstone',async()=>{
  const {document,...base}=post;await control('publish',{event:{...base,event_id:randomUUID(),revision:'3',type:'content.withdrawn'}});await control('relay');await control('step');
  await eventually(async()=>await dbCount('statuses','uri=$1',[objectId])===0,'Mastodon Delete');assert.equal(await control('tombstone',{id:objectId}),410);
  assert.ok([404,410].includes((await control<{status:number}>('peer-status',{id:statusId})).status));deleted=true;
 });
 assert.ok(deleted,'Delete prerequisite failed');
 await t.test('Mastodon signs Undo Follow and content removes the relationship',async()=>{
  await rails("UnfollowService.new.call(Account.find_local!('alice'),Account.find_by!(username:'editor',domain:'federation.example'))");
  await eventually(async()=>(await snapshot()).queue.queued>0,'Mastodon Undo');await control('step');assert.equal((await snapshot()).followers,0);
  assert.equal(await control('replay-undo'),202);await control('step');assert.equal((await snapshot()).followers,0);undone=true;
 });
 assert.ok(undone,'Undo prerequisite failed');
 console.log(JSON.stringify({peer:'Mastodon 5.0.0-nightly.2026-10-22',result:await snapshot()}));
 }catch(error){console.error(JSON.stringify(await admin!.db.query('SELECT uri,text,spoiler_text,edited_at FROM public.statuses')));console.error(JSON.stringify(await snapshot()));console.error(await logs(endpoint));console.error(await logs(worker));console.error(await logs(web));throw error;}
});
