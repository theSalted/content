import {spawn} from 'node:child_process';
import {mkdtemp,writeFile,chmod,rm} from 'node:fs/promises';
import {join} from 'node:path';
import {tmpdir} from 'node:os';

export function deliveryEnvironment(input,caPath){
 if(input.CMS_DELIVERY_ENABLED!=='true')throw new Error('Explicit CMS delivery enablement required');
 for(const key of ['CMS_DELIVERY_DATABASE_URL','CMS_DELIVERY_SCHEMA','CMS_DELIVERY_INSTALLATION_ID','PUBLICATION_ID','PUBLISHER_TOKEN'])if(!input[key])throw new Error('Missing scoped delivery setting');
 if(input.CMS_DELIVERY_DATABASE_URL===input.DATABASE_URL)throw new Error('CMS and AP roles must differ');
 if(!/^[a-z][a-z0-9_]{0,62}$/.test(input.CMS_DELIVERY_SCHEMA)||input.CMS_DELIVERY_SCHEMA===input.ACTIVITYPUB_SCHEMA)throw new Error('Distinct CMS namespace required');
 const port=Number(input.INTERNAL_PORT);if(!Number.isInteger(port)||port<1||port>65535||port===Number(input.PORT))throw new Error('Separate private listener required');
 const archive=input.CMS_ARCHIVE_DELIVERY_ENABLED??'false';if(!['true','false'].includes(archive)||archive==='true'&&input.ACTIVITYPUB_ARCHIVE_ENABLED!=='true')throw new Error('Archive gates disagree');
 const resolution=input.CMS_RECEIPT_RESOLUTION_ENABLED??'false';if(!['true','false'].includes(resolution))throw new Error('Explicit receipt resolution setting required');
 const database=new URL(input.CMS_DELIVERY_DATABASE_URL);
 if(!['postgres:','postgresql:'].includes(database.protocol)||database.searchParams.get('sslmode')!=='verify-full')throw new Error('Verified TLS required');
 if(caPath)database.searchParams.set('sslrootcert',caPath);
 else if(database.searchParams.has('sslrootcert'))throw new Error('Container CA material required');
 return {
  PATH:input.PATH??'/usr/local/bin:/usr/bin:/bin',GOMAXPROCS:'1',GOMEMLIMIT:'64MiB',
  CONTENT_PUBLISHING_DELIVERY_ENABLED:'true',CONTENT_DELIVERY_DATABASE_URL:database.toString(),
  CONTENT_SCHEMA:input.CMS_DELIVERY_SCHEMA,CONTENT_INSTALLATION_ID:input.CMS_DELIVERY_INSTALLATION_ID,
  CONTENT_PUBLICATION_ID:input.PUBLICATION_ID,CONTENT_PUBLISHING_DESTINATION:'activitypub',
  CONTENT_PUBLISHING_ENDPOINT:`http://127.0.0.1:${port}/internal/v2/events`,
  CONTENT_FEDERATION_PROJECTION_ENABLED:resolution,...(resolution==='true'?{CONTENT_RECEIPT_ENDPOINT:`http://127.0.0.1:${port}/internal/v2/receipts/resolve`}:{}),
  CONTENT_PUBLISHING_TOKEN:input.PUBLISHER_TOKEN,CONTENT_PUBLISHING_ARCHIVE_DELIVERY_ENABLED:archive,...(archive==='true'?{CONTENT_PUBLISHING_ARCHIVE_ENDPOINT:`http://127.0.0.1:${port}/internal/v2/archive`}:{})
 };
}
export function progressState(archive=false,now=()=>performance.now(),resolution=false){
 const lanes=['live',...(archive?['archive']:[]),...(resolution?['resolution']:[])];let last={},running=false,paused=false,queue,queueAt,resolutionQueue,resolutionQueueAt;
 return {start:()=>{last={};running=true;paused=false},tick:lane=>{if(lanes.includes(lane))last[lane]=now()},fail:()=>{running=false;last={}},pause:()=>{running=false;paused=true},queue:value=>{queue=value;queueAt=now()},
  resolutionQueue:value=>{resolutionQueue=value;resolutionQueueAt=now()},
  snapshot:()=>({ready:running&&lanes.every(lane=>last[lane]!==undefined&&now()-last[lane]<45000),running,paused,lanes:Object.fromEntries(lanes.map(lane=>[lane,last[lane]===undefined?null:Math.floor((now()-last[lane])/1000)])),queue:queue??null,queue_age_seconds:queueAt===undefined?null:Math.floor((now()-queueAt)/1000),resolution_queue:resolutionQueue??null,resolution_queue_age_seconds:resolutionQueueAt===undefined?null:Math.floor((now()-resolutionQueueAt)/1000)})};
}
export function queueCounts(value){
 if(!value||typeof value!=='object')throw new Error('queue format');
 const result={};for(const lane of ['live','archive']){
  const q=value[lane];if(!q||typeof q!=='object')throw new Error('queue lane');result[lane]={};
  for(const key of ['pending','leased','quarantined','accepted']){if(!Number.isSafeInteger(q[key])||q[key]<0)throw new Error('queue count');result[lane][key]=q[key];}
 }
 if(!Number.isSafeInteger(value.blocked_streams)||value.blocked_streams<0)throw new Error('blocked count');result.blocked_streams=value.blocked_streams;return result;
}
// Runtime dependency arguments are used only by local tests, never environment
// values. The deployed binary/path, command and retry budget are fixed.
export async function startDelivery(input,{spawnChild=spawn,now=()=>performance.now(),log=()=>{},schedule=setTimeout,unschedule=clearTimeout}={}){
 const archive=input.CMS_ARCHIVE_DELIVERY_ENABLED==='true',resolution=input.CMS_RECEIPT_RESOLUTION_ENABLED==='true',state=progressState(archive,now,resolution);let directory,current,stopping=false,timer,attempts=[],failures=0;
 const failLog=event=>log({event});
 try{
  const ca=input.CMS_DELIVERY_DATABASE_SSL_CA;
  if(ca){directory=await mkdtemp(join(tmpdir(),'content-delivery-'));await chmod(directory,0o700);await writeFile(join(directory,'ca.pem'),ca,{mode:0o600});}
  const env=deliveryEnvironment(input,directory&&join(directory,'ca.pem'));
  const launch=()=>{
   if(stopping)return;timer=undefined;let child;
   try{child=spawnChild('/app/content-delivery',['deliver'],{env,stdio:['ignore','pipe','pipe']});}catch{state.fail();retry();return;}
   state.start();let buffer='',invalid=false;const item={child,done:undefined};current=item;
   item.done=new Promise(resolve=>{
    child.once('error',()=>{state.fail();});
    child.once('close',()=>{state.fail();if(current===item)current=undefined;resolve();if(!stopping)retry();});
   });
   const reject=()=>{if(invalid)return;invalid=true;state.fail();failLog('delivery_protocol_rejected');child.kill('SIGTERM');};
   child.stdout.on('data',chunk=>{
    if(invalid)return;buffer+=chunk.toString('utf8');if(buffer.length>16384){reject();return;}
    let cut;while((cut=buffer.indexOf('\n'))>=0){const line=buffer.slice(0,cut);buffer=buffer.slice(cut+1);let x;try{x=JSON.parse(line);}catch{reject();return;}
     if(x.protocol!=='content-delivery-supervisor-v1'||!['live','archive','all',...(resolution?['resolution']:[])].includes(x.lane)||typeof x.at!=='string'||!Number.isFinite(Date.parse(x.at))){reject();return;}
     if(x.event==='started'&&x.archive_enabled===archive&&x.pool_limit===2&&(x.resolution_enabled??false)===resolution){failLog('cms_delivery_started');continue;}
     if(x.event==='progress'&&['live',...(archive?['archive']:[]),...(resolution?['resolution']:[])].includes(x.lane)){state.tick(x.lane);continue;}
     if(x.event==='outcome'&&/^[a-f0-9-]{36}$/.test(x.event_id)&&Number.isSafeInteger(x.attempt)&&x.attempt>0&&['accepted','retry','quarantined'].includes(x.state)&&/^[a-z_]{0,64}$/.test(x.code??'')&&/^[a-z_]{0,32}$/.test(x.acceptance??'')){
      log({event:'cms_delivery_outcome',lane:x.lane,event_id:x.event_id,attempt:x.attempt,state:x.state,code:x.code,acceptance:x.acceptance});continue;
     }
     if(resolution&&x.event==='resolution_outcome'&&x.lane==='resolution'&&/^[a-f0-9-]{36}$/.test(x.event_id)&&Number.isSafeInteger(x.attempt)&&x.attempt>0&&['complete','retry','quarantined'].includes(x.state)&&/^[a-z_]{0,64}$/.test(x.code??'')){
      log({event:'cms_receipt_resolution_outcome',event_id:x.event_id,attempt:x.attempt,state:x.state,code:x.code});continue;
     }
     if(resolution&&x.event==='resolution_queue_status'&&x.lane==='resolution'){
      try{const q=resolutionCounts(x.queue);state.resolutionQueue(q);log({event:'cms_receipt_resolution_queue',queue:q});}catch{reject();return;}continue;
     }
     if(x.event==='queue_status'){try{const q=queueCounts(x.queue);state.queue(q);log({event:'cms_delivery_queue',queue:q,publisher_ready:state.snapshot().ready,progress_age_seconds:state.snapshot().lanes});}catch{reject();return;}continue;}
     if(x.event==='status_unavailable'){failLog('cms_delivery_status_unavailable');continue;}
     if(x.event==='stopped'&&typeof x.failed==='boolean'){state.fail();continue;}
     reject();return;
    }
   });
   child.stderr.resume();
  };
  const retry=()=>{
   state.fail();attempts=attempts.filter(at=>now()-at<600000);
   if(attempts.length>=5){state.pause();failLog('cms_delivery_restart_budget_exhausted');return;}
   const delay=Math.min(60000,5000*2**Math.min(failures++,4));attempts.push(now());failLog('cms_delivery_restart_scheduled');timer=schedule(launch,delay);timer?.unref?.();
  };
  launch();
  return {status:state.snapshot,ready:()=>state.snapshot().ready,close:async()=>{
   stopping=true;if(timer)unschedule(timer);state.fail();const c=current;
   if(c){c.child.kill('SIGTERM');const force=setTimeout(()=>c.child.kill('SIGKILL'),15000);force.unref();await c.done;clearTimeout(force);}
   if(directory)await rm(directory,{recursive:true,force:true});
  }};
 }catch{stopping=true;if(timer)unschedule(timer);if(current)current.child.kill('SIGKILL');if(directory)await rm(directory,{recursive:true,force:true});throw new Error('Scoped CMS delivery startup refused');}
}

export function resolutionCounts(value){
 if(!value||typeof value!=='object')throw new Error('resolution queue format');
 const result={};for(const key of ['pending','leased','complete','quarantined']){if(!Number.isSafeInteger(value[key])||value[key]<0)throw new Error('resolution queue count');result[key]=value[key];}return result;
}
