import { serve } from '@hono/node-server';
import { createModule } from './module.ts';
import { processSettings,shutdownDeadline } from './process-settings.ts';
import {splitIngress} from './ingress.ts';

try {
  const config=processSettings(process.env);
  const app=await createModule(process.env);
  let shuttingDown=false;
  const ingress=splitIngress(request=>shuttingDown
    ?new Response(null,{status:503,headers:{'cache-control':'no-store'}}):app.fetch(request),config.internalPort!==undefined);
  const servers=[serve({fetch:ingress.publicFetch,port:config.port,hostname:config.host})];
  if(config.internalPort!==undefined)servers.push(serve({fetch:ingress.privateFetch,port:config.internalPort,hostname:config.host}));
  console.info(JSON.stringify({event:'server_started',port:config.port,federation_enabled:process.env.FEDERATION_ENABLED==='true'}));
  const stop=async()=>{
    if(shuttingDown)return;shuttingDown=true;
    const clearDeadline=shutdownDeadline(config.shutdownGraceMs);
    try{await Promise.all(servers.map(server=>new Promise<void>((resolve,reject)=>server.close(error=>error?reject(error):resolve()))));await app.close();}
    catch{console.error('API shutdown failed.');process.exitCode=1;}
    finally{clearDeadline();}
  };
  process.once('SIGTERM',()=>void stop());process.once('SIGINT',()=>void stop());
} catch {
  console.error('Startup refused: check configuration, schema, existing identity and fingerprint. No identity was generated.');
  process.exitCode=1;
}
