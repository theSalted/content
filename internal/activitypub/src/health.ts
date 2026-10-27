// Readiness is bounded and single-flight: repeated probes cannot exhaust a
// connection pool while a database is unavailable. Driver timeouts still own
// cancellation of the underlying query; the HTTP deadline does not cancel it.
export function readinessProbe(check:()=>Promise<boolean>, timeoutMs=2000) {
  let pending:Promise<boolean>|undefined;
  return async()=>{
    if(!pending) pending=Promise.resolve().then(check).catch(()=>false).finally(()=>{pending=undefined;});
    let timer:ReturnType<typeof setTimeout>|undefined;
    try {return await Promise.race([pending,new Promise<boolean>(resolve=>{timer=setTimeout(()=>resolve(false),timeoutMs);})]);}
    finally {clearTimeout(timer);}
  };
}
export function healthHandler(check:()=>Promise<boolean>, options:{disabled?:boolean;timeoutMs?:number}={}) {
  const ready=readinessProbe(check,options.timeoutMs);
  let draining=false;
  return {drain:()=>{draining=true;},fetch:async(request:Request):Promise<Response|undefined>=>{
    const path=new URL(request.url).pathname;
    if(path!=='/health/live'&&path!=='/health/ready')return;
    const headers={'cache-control':'no-store'};
    if(request.method!=='GET'&&request.method!=='HEAD')return new Response(null,{status:405,headers:{...headers,allow:'GET, HEAD'}});
    const live=path==='/health/live';
    const ok=live||(!draining&&(options.disabled||await ready()));
    const status=live?'up':draining?'draining':options.disabled?'disabled':ok?'ready':'unavailable';
    return request.method==='HEAD'?new Response(null,{status:ok?200:503,headers}):Response.json({status},{status:ok?200:503,headers});
  }};
}
// This is per process, unlike the shared publication heartbeat. Another
// replica cannot make an unhealthy worker ready. A completed idle poll counts.
export function workerProgress(now=()=>performance.now(),maxAgeMs=120000) {
  const progress:{relay?:number;transport?:number}={};
  return {tick:(loop:'relay'|'transport')=>{progress[loop]=now();},ready:()=>{
    const at=now();return ['relay','transport'].every(loop=>{
      const last=progress[loop as keyof typeof progress];return last!==undefined&&at-last<=maxAgeMs;
    });
  }};
}
