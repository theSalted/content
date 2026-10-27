type Fetcher=(request:Request)=>Promise<Response>|Response;
const unavailable=()=>new Response(null,{status:404,headers:{'cache-control':'no-store'}});
// Enable this split whenever the API has a public ingress. Only the public
// listener is exposed by the reverse proxy; publishing remains authenticated
// on the private listener. A single private listener remains supported.
export function splitIngress(fetcher:Fetcher,split:boolean){
 return {
  publicFetch:(request:Request)=>split&&new URL(request.url).pathname.startsWith('/internal/')?unavailable():fetcher(request),
  privateFetch:(request:Request)=>{
   const path=new URL(request.url).pathname;
   return path.startsWith('/internal/')||path==='/health/live'||path==='/health/ready'?fetcher(request):unavailable();
  }
 };
}
