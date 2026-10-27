import {createHash,timingSafeEqual} from 'node:crypto';
import {serve} from '@hono/node-server';
import {createModule} from '../../src/module.ts';
import {processSettings,shutdownDeadline} from '../../src/process-settings.ts';
import {splitIngress} from '../../src/ingress.ts';
import {startDelivery} from './delivery-child.mjs';

// Opt-in integrated runtime. The ordinary API/worker entrypoints remain usable.
// Public publishing routes remain rejected by splitIngress.
let app:any,delivery:any,servers:any[]=[],shuttingDown=false,stopping:Promise<void>|undefined;
function stop(failed=false):Promise<void>{
 if(failed)process.exitCode=1;
 if(stopping)return stopping;
 shuttingDown=true;
 stopping=(async()=>{const clear=shutdownDeadline(20000);try{
  if(delivery)await delivery.close();
  await Promise.all(servers.map(server=>new Promise<void>((resolve,reject)=>server.close((e:Error)=>e?reject(e):resolve()))));
  if(app)await app.close();
 }catch{process.exitCode=1;console.error('Integrated runtime shutdown failed.');}finally{clear();}})();return stopping;
}
try{
 const config=processSettings(process.env);
 if(process.env.FEDERATION_ENABLED!=='true'||config.internalPort===undefined||process.env.CMS_DELIVERY_ENABLED!=='true')throw new Error('Explicit integrated configuration required');
 app=await createModule(process.env);
 const ingress=splitIngress(async(request:Request)=>{
  if(shuttingDown)return new Response(null,{status:503,headers:{'cache-control':'no-store'}});
  if(new URL(request.url).pathname==='/internal/v2/publishing-health'){
   const expected=createHash('sha256').update('Bearer '+process.env.PUBLISHER_TOKEN).digest(),actual=createHash('sha256').update(request.headers.get('authorization')??'').digest();
   if(!timingSafeEqual(expected,actual))return new Response(null,{status:401});
   if(request.method!=='GET')return new Response(null,{status:405,headers:{allow:'GET'}});
   const status=delivery?.status()??{ready:false,running:false,paused:false};
   return Response.json(status,{status:status.ready?200:503,headers:{'cache-control':'no-store'}});
  }
  return app.fetch(request);
 },true);
 servers=[serve({fetch:ingress.publicFetch,port:config.port,hostname:config.host}),serve({fetch:ingress.privateFetch,port:config.internalPort,hostname:config.host})];
 await Promise.all(servers.map(server=>server.listening?Promise.resolve():new Promise<void>((resolve,reject)=>{server.once('listening',resolve);server.once('error',reject);})));
 delivery=await startDelivery(process.env,{log:(item:object)=>console.info(JSON.stringify(item))});
 process.once('SIGTERM',()=>void stop());process.once('SIGINT',()=>void stop());
 console.info(JSON.stringify({event:'integrated_runtime_started',cms_delivery:true}));
}catch{console.error('Integrated runtime startup refused (details redacted).');await stop(true);}
