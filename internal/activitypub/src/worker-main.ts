import {serve} from '@hono/node-server';
import {readConfig} from './config.ts';
import {runtime} from './runtime.ts';
import {enqueueIntent} from './federation.ts';
import {relayLoop} from './worker.ts';
import {healthHandler,workerProgress} from './health.ts';
import {processSettings,shutdownDeadline} from './process-settings.ts';

try {
  const settings=processSettings(process.env);
  const enabled=process.env.FEDERATION_ENABLED==='true';
  const progress=workerProgress();
  const app=enabled?await runtime(readConfig(process.env),()=>progress.tick('transport')):undefined;
  const controller=new AbortController();
  const health=healthHandler(async()=>{
    if(!progress.ready())return false;
    await app!.state.store.db.query('SELECT 1');return true;
  },{disabled:!enabled});
  const server=serve({fetch:async request=>await health.fetch(request)??new Response(null,{status:404}),port:settings.port,hostname:settings.host});
  let clearDeadline:(()=>void)|undefined;
  const stop=()=>{if(controller.signal.aborted)return;health.drain();clearDeadline=shutdownDeadline(settings.shutdownGraceMs);controller.abort();};
  process.once('SIGTERM',stop);process.once('SIGINT',stop);
  console.info(JSON.stringify({event:'worker_started',port:settings.port,federation_enabled:enabled}));
  const loops=app?[
    app.federation.startQueue(app.state,{signal:controller.signal}),
    relayLoop(app.state.store,job=>enqueueIntent(app.federation,app.state,job),controller.signal,()=>progress.tick('relay'))
  ]:[new Promise<void>(resolve=>controller.signal.addEventListener('abort',()=>resolve(),{once:true}))];
  try{await Promise.all(loops);}finally{
    stop();await Promise.allSettled(loops);
    await new Promise<void>((resolve,reject)=>server.close(error=>error?reject(error):resolve()));
    await app?.close();clearDeadline?.();
  }
} catch {
  console.error('Federation worker stopped: inspect queue health and configuration; no keys were generated.');
  process.exitCode=1;
}
