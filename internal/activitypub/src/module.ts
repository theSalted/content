import {readConfig} from './config.ts';
import {runtime} from './runtime.ts';
import {makeApp} from './app.ts';
import {healthHandler} from './health.ts';
// Disabled module performs no DB connection, key lookup or generation. The Go
// CMS has no dependency on this process and keeps its own readiness endpoint.
export async function createModule(env:NodeJS.ProcessEnv){
 if(env.FEDERATION_ENABLED!=='true'){
 const health=healthHandler(async()=>false,{disabled:true});
 return {fetch:async(request:Request)=>await health.fetch(request)??Response.json({error:'activitypub_disabled'},{status:503}),close:async()=>{}};
 }
 const app=await runtime(readConfig(env));
 return {fetch:makeApp(app.state,app.federation),close:app.close};
}
