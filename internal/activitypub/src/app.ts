import {projectArticleMedia} from './media-origins.ts';
import {z} from 'zod';
import {resolveReceipt} from './receipts.ts';
import { createHash, timingSafeEqual } from 'node:crypto';
import { respondWithObjectIfAcceptable } from '@fedify/fedify';
import { Activity, Article, Tombstone } from '@fedify/vocab';
import { Temporal } from 'temporal-polyfill';
import type { Federation } from '@fedify/fedify';
import type { FederationState } from './federation.ts';
import { publishingEvent, archiveSnapshot, ServiceError } from './contracts.ts';
import { inboxSignatureHeaders } from './inbox-signature-policy.ts';
import {FeedbackStore,moderation,actorBlock} from './feedback.ts';
import {publishingProfile,moderationContract} from './capabilities.ts';
import {withFeedbackCollections} from './profile.ts';
import {healthHandler} from './health.ts';

const MAX_BODY = 2*1024*1024;
export function makeApp(state: FederationState, federation: Federation<FederationState>) {
  const feedback=new FeedbackStore(state.store.db);
  const health=healthHandler(async()=>{await state.store.db.query('SELECT 1');return state.config.federationEnabled;});
  return async (request: Request): Promise<Response> => {
    const url = new URL(request.url);
    try {
      const healthResponse=await health.fetch(request);if(healthResponse)return healthResponse;
      if (url.pathname === '/internal/v2/events' || url.pathname === '/internal/v2/archive') {
        const archive=url.pathname.endsWith('/archive');
        if (request.method !== 'POST') return new Response(null,{status:405,headers:{Allow:'POST'}});
        if (!authorized(request,state.config.publisherToken)) throw new ServiceError(401,'unauthorized');
        if(archive&&!state.config.archiveEnabled)throw new ServiceError(403,'archive_disabled');
        if (!/^application\/json(?:;|$)/i.test(request.headers.get('content-type')??'')) throw new ServiceError(415,'json_required');
        const body = await readLimited(request);
        let value: unknown;
        try { value = JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(body)); } catch { throw new ServiceError(400,'invalid_json'); }
        const parsed = archive ? archiveSnapshot.safeParse(value) : publishingEvent.safeParse(value);
        if (!parsed.success) return Response.json({error:'invalid_event',fields:parsed.error.issues.map(i=>i.path.join('.'))},{status:422});
        return Response.json(parsed.data.type==='content.archived' ? await state.store.archive(parsed.data,state.config.publisherId) : await state.store.publish(parsed.data,state.config.publisherId),{status:202});
      }
      if(url.pathname==='/internal/v2/receipts/resolve'){
        if(request.method!=='POST')return new Response(null,{status:405,headers:{Allow:'POST'}});
        if(!authorized(request,state.config.publisherToken))throw new ServiceError(401,'unauthorized');
        if(!/^application\/json(?:;|$)/i.test(request.headers.get('content-type')??''))throw new ServiceError(415,'json_required');
        let value:unknown;try{value=JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(await readLimited(request)));}catch(error){if(error instanceof ServiceError)throw error;throw new ServiceError(400,'invalid_json');}
        const parsed=z.strictObject({schema_version:z.literal(1),event:z.union([publishingEvent,archiveSnapshot])}).safeParse(value);
        if(!parsed.success)throw new ServiceError(422,'invalid_receipt_query');
        return Response.json(await resolveReceipt(state.store,parsed.data.event,state.config.publisherId),{headers:{'cache-control':'no-store'}});
      }
      if (url.pathname === '/internal/v2/status') {
        if (!authorized(request,state.config.publisherToken)) throw new ServiceError(401,'unauthorized');
        if (request.method !== 'GET') return new Response(null,{status:405});
        return Response.json({ publishing_profile:publishingProfile,moderation_contract:moderationContract,archive_enabled:!!state.config.archiveEnabled,feedback_enabled:!!state.config.feedbackEnabled,delivery_enabled:state.config.federationEnabled,
          relay_recent:(await state.store.db.query("SELECT 1 FROM {{schema}}.worker_heartbeats WHERE name='federation' AND updated_at > now()-interval '60 seconds'")).length>0,
          intents:await state.store.db.query('SELECT state,count(*) AS count FROM {{schema}}.outbound_intents GROUP BY state'),
          transport:await state.store.db.query('SELECT dead,count(*) AS count,max(attempts) AS attempts_max FROM {{schema}}.transport_jobs GROUP BY dead'),
          delivery_failures:await state.store.db.query('SELECT error_code,count(*) AS count FROM {{schema}}.delivery_failures GROUP BY error_code'),
          note:'enqueued means persisted in the transport queue; it does not confirm remote delivery' });
      }
      if(url.pathname==='/internal/v2/feedback'||url.pathname==='/internal/v2/feedback/moderate'||url.pathname==='/internal/v2/feedback/block'){
        if(!authorized(request,state.config.moderationToken))throw new ServiceError(401,'unauthorized');
        if(!state.config.feedbackEnabled)throw new ServiceError(403,'feedback_disabled');
        if(url.pathname==='/internal/v2/feedback'&&request.method==='GET'){
          const cursor=url.searchParams.get('cursor')??'0',limit=url.searchParams.get('limit')??'50';
          if(!/^\d{1,7}$/.test(cursor)||!/^\d{1,3}$/.test(limit)||Number(limit)<1||Number(limit)>100)throw new ServiceError(400,'invalid_feedback_page');
          const rows=await feedback.list(url.searchParams.get('state')??'pending',Number(cursor),Number(limit)+1);
          return Response.json({items:rows.slice(0,Number(limit)),next_cursor:rows.length>Number(limit)?String(Number(cursor)+Number(limit)):null},{headers:{'cache-control':'no-store'}});
        }
        if(request.method!=='POST')return new Response(null,{status:405});
        if(!/^application\/json(?:;|$)/i.test(request.headers.get('content-type')??''))throw new ServiceError(415,'json_required');
        const raw=await readLimited(request);let value:unknown;try{value=JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(raw));}catch{throw new ServiceError(400,'invalid_json');}
        if(url.pathname.endsWith('/moderate')){const parsed=moderation.safeParse(value);if(!parsed.success)throw new ServiceError(422,'invalid_moderation');await feedback.moderate(parsed.data.id,parsed.data.decision,parsed.data.expected_activity_id);}
        else if(url.pathname.endsWith('/block')){const parsed=actorBlock.safeParse(value);if(!parsed.success)throw new ServiceError(422,'invalid_actor_block');await feedback.block(parsed.data.actor,parsed.data.blocked);}
        else return new Response(null,{status:405});
        return Response.json({result:'applied'},{headers:{'cache-control':'no-store'}});
      }
      if (url.pathname.startsWith('/internal/')) return new Response(null,{status:404});
      const inbox = url.pathname==='/inbox' || url.pathname===new URL(state.config.actorId).pathname+'/inbox';
      if (request.method==='POST') {
        if (!inbox) return new Response(null,{status:405,headers:{Allow:'GET, HEAD'}});
        if (!state.config.federationEnabled) throw new ServiceError(503,'federation_disabled');
        // Bound bytes before JSON-LD parsing/signature lookup. Preserve original bytes for signature verification.
        const raw = await readLimited(request);
        request = new Request(request.url,{method:'POST',headers:inboxSignatureHeaders(request.headers),body:raw.buffer as ArrayBuffer});
      } else if (request.method!=='GET' && request.method!=='HEAD') return new Response(null,{status:405});
      // Human readers keep the canonical website permalink. AP clients still
      // receive the persistent object, and withdrawals never redirect to old HTML.
      const accept=request.headers.get('accept')??'';
      if((request.method==='GET'||request.method==='HEAD')&&accept.includes('text/html')&&!accept.includes('application/activity+json')&&!accept.includes('application/ld+json')){
        const canonical=new URL(url.pathname,state.config.origin).href;
        const row=await state.store.object(canonical);
        if(row?.published_at){
          if(!row.payload)return new Response('This article is no longer public.',{status:410,headers:{'content-type':'text/plain; charset=utf-8','cache-control':'no-store',vary:'Accept'}});
          const target=row.payload.url;
          if(typeof target==='string'&&target!==canonical)return new Response(null,{status:302,headers:{location:target,'cache-control':'no-store',vary:'Accept'}});
        }
      }
      // One trusted canonical public origin; never trust forwarded-host input to mint identity or verify target URI.
      const canonicalUrl=new URL(url.pathname+url.search,state.config.origin);
      const canonicalHeaders=new Headers(request.headers);
      canonicalHeaders.set('host',new URL(state.config.origin).host);
      request=new Request(canonicalUrl,new Request(request,{headers:canonicalHeaders}));
      const dynamicProfile=url.pathname===new URL(state.config.actorId).pathname&&!!state.config.imageUrls?.length;
      const dynamicMedia=!!state.config.publicMedia&&!url.pathname.startsWith('/ap/activities/');
      if(dynamicProfile||dynamicMedia){request.headers.delete('if-none-match');request.headers.delete('if-modified-since');}
      const response=await federation.fetch(request,{contextData:state,
        onNotFound:async req => {
          // Exact imported object IDs may predate /ap/objects/{id}; preserve their paths without minting new IDs.
          const canonical = new URL(new URL(req.url).pathname,state.config.origin).href;
          const row = await state.store.object(canonical);
          if (row?.published_at) {
            const object = row.payload ? await Article.fromJsonLd(withFeedbackCollections(projectArticleMedia(row.payload,state.config.publicMedia),state.config.feedbackEnabled)) : new Tombstone({id:new URL(row.object_id),formerType:Article,deleted:Temporal.Instant.from(new Date(row.changed_at).toISOString())});
            const response = await respondWithObjectIfAcceptable(object,req) ?? new Response(null,{status:406});
            return !row.payload && response.status===200 ? new Response(response.body,{status:410,headers:response.headers}) : response;
          }
          if (url.pathname.startsWith('/ap/activities/')) {
            const activity = (await state.store.db.query<{payload:Record<string,unknown>}>('SELECT payload FROM {{schema}}.activities WHERE id=$1 AND public',[canonical]))[0];
            if (activity) return await respondWithObjectIfAcceptable(await Activity.fromJsonLd(activity.payload),req) ?? new Response(null,{status:406});
          }
          return new Response(null,{status:404});
        },onNotAcceptable:()=>new Response(null,{status:406}) });
      if(dynamicProfile||dynamicMedia||state.config.feedbackEnabled&&url.pathname.startsWith('/ap/objects/')){
        const headers=new Headers(response.headers);headers.set('cache-control','no-store');headers.delete('etag');headers.delete('last-modified');
        return new Response(request.method==='HEAD'?null:response.body,{status:response.status,headers});
      }
      return response;
    } catch (error) {
      if (error instanceof ServiceError) return Response.json({error:error.code},{status:error.status});
      // Never leak database URLs, request bodies, private keys or remote response bodies.
      console.error(JSON.stringify({event:'request_failed',path:url.pathname}));
      return Response.json({error:'temporarily_unavailable'},{status:503});
    }
  };
}
function authorized(request: Request, token: string|undefined) {
  if(!token)return false;
  const expected = createHash('sha256').update(`Bearer ${token}`).digest();
  const actual = createHash('sha256').update(request.headers.get('authorization')??'').digest();
  return timingSafeEqual(expected,actual);
}
async function readLimited(request: Request): Promise<Uint8Array> {
  const reader = request.body?.getReader();
  if (!reader) return new Uint8Array();
  let size=0;
  const chunks:Uint8Array[]=[];
  try {
    for (;;) {
      const {done,value}=await reader.read();
      if (done) break;
      size+=value.byteLength;
      if (size>MAX_BODY) { await reader.cancel(); throw new ServiceError(413,'body_too_large'); }
      chunks.push(value);
    }
  } finally { reader.releaseLock(); }
  return new Uint8Array(Buffer.concat(chunks));
}
