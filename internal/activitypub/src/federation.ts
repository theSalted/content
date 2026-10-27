import {projectArticleMedia,projectOutboxMedia} from './media-origins.ts';
import { createFederation, SendActivityError } from '@fedify/fedify';
import type { KvStore, MessageQueue, Federation, InboxContext } from '@fedify/fedify';
import { Activity, Article, Endpoints, Follow, Person, Image, PropertyValue, Tombstone, Undo, Create, Update, Delete, Like, Announce, Note, Object as ASObject } from '@fedify/vocab';
import { Temporal } from 'temporal-polyfill';
import { getDocumentLoader, type DocumentLoader } from '@fedify/vocab-runtime';
import type { Config } from './config.ts';
import type { Identity } from './identity.ts';
import { Store, PUBLIC } from './store.ts';
import {FeedbackStore} from './feedback.ts';
import {chooseBanner,withFeedbackCollections} from './profile.ts';
import type { Intent } from './store.ts';
import { ServiceError } from './contracts.ts';

export type FederationState = { store: Store; identity: Identity; config: Config };
export function makeFederation(state: FederationState, kv: KvStore, queue: MessageQueue, fixtureLoader?: DocumentLoader): Federation<FederationState> {
  const federation = createFederation<FederationState>({
    origin: state.config.origin, kv, queue, manuallyStartQueue: true,
    ...(fixtureLoader ? {documentLoaderFactory:()=>fixtureLoader,authenticatedDocumentLoaderFactory:()=>fixtureLoader,contextLoaderFactory:()=>getDocumentLoader()} : {}),
    onOutboxError: async (error, activity) => {
      await state.store.db.query('INSERT INTO {{schema}}.delivery_failures(activity_id,error_code) VALUES($1,$2)',
        [activity?.id?.href??null, error instanceof SendActivityError ? `http_${error.statusCode}` : 'transport_error']);
    }
  });
  federation.setActorDispatcher(state.config.actorRoute, async (ctx, identifier) => {
    if (identifier !== state.config.identifier) return null;
    const keys = await ctx.getActorKeyPairs(identifier);
    if (keys[0]?.keyId.href !== state.identity.keyId) throw new Error('Fedify key ID differs from imported identity');
    const banner=chooseBanner(state.config);
    return new Person({ id: new URL(state.identity.actorId), name: state.config.displayName, preferredUsername: identifier,
      summary:state.config.summary,
      icon:state.config.iconUrl?new Image({url:new URL(state.config.iconUrl),name:state.config.iconAlt}):undefined,
      image:banner?new Image({url:new URL(banner),name:state.config.imageAlt}):undefined,
      attachments:[new PropertyValue({name:'Website',value:'<a href="'+escapeHtml(state.config.origin)+'/" rel="me">'+escapeHtml(state.config.origin)+'</a>'})],
      url: new URL(state.config.profileUrl), inbox: ctx.getInboxUri(identifier), outbox: ctx.getOutboxUri(identifier),
      followers: ctx.getFollowersUri(identifier), following: ctx.getFollowingUri(identifier),
      endpoints: new Endpoints({ sharedInbox: ctx.getInboxUri() }),
      publicKeys: keys.map(key => key.cryptographicKey), assertionMethods: keys.map(key => key.multikey) });
  }).setKeyPairsDispatcher((_ctx, identifier) => identifier === state.config.identifier
    ? [{ privateKey: state.identity.privateKey, publicKey: state.identity.publicKey }] : []);

  federation.setFollowersDispatcher(`${state.config.actorRoute}/followers`, async (_ctx, identifier, cursor) => {
    if (identifier !== state.config.identifier) return null;
    const offset = parseCursor(cursor);
    const rows = await state.store.followers(offset,21);
    return { items: rows.slice(0,20).map(row => ({id:new URL(row.actor_id),inboxId:new URL(row.inbox),endpoints:{sharedInbox:row.shared_inbox?new URL(row.shared_inbox):null}})), nextCursor: rows.length>20?String(offset+20):null };
  }).setFirstCursor((_ctx, identifier) => identifier === state.config.identifier ? '0' : null)
    .setCounter(async (_ctx, identifier) => identifier === state.config.identifier ? Number((await state.store.db.query('SELECT count(*) AS count FROM {{schema}}.followers'))[0]!.count) : null);
  // Even an empty public collection needs a first page. Mastodon treats a
  // missing first link on either relationship collection as hide_collections.
  federation.setFollowingDispatcher(`${state.config.actorRoute}/following`, (_ctx, identifier) => identifier === state.config.identifier ? {items:[]} : null)
    .setFirstCursor((_ctx, identifier) => identifier === state.config.identifier ? '0' : null)
    .setCounter((_ctx, identifier) => identifier === state.config.identifier ? 0 : null);
  federation.setOutboxDispatcher(`${state.config.actorRoute}/outbox`, async (_ctx, identifier, cursor) => {
    if (identifier !== state.config.identifier) return null;
    const offset = parseCursor(cursor);
    const rows = await state.store.outbox(offset,21);
    return { items: await Promise.all(rows.slice(0,20).map(row => Activity.fromJsonLd(projectOutboxMedia(row.payload,state.config.publicMedia)))), nextCursor: rows.length>20?String(offset+20):null };
  }).setFirstCursor((_ctx, identifier) => identifier === state.config.identifier ? '0' : null)
    .setCounter(async (_ctx, identifier) => identifier === state.config.identifier ? state.store.outboxCount() : null);
  federation.setObjectDispatcher(Article, '/ap/objects/{publication}/{id}', async (ctx, { id, publication }) => {
    if(publication!==state.config.publicationId)return null;
    const row = await state.store.object(ctx.getObjectUri(Article,{publication,id}).href);
    if (!row || !row.published_at) return null;
    if (row.payload) return Article.fromJsonLd(withFeedbackCollections(projectArticleMedia(row.payload,state.config.publicMedia),state.config.feedbackEnabled));
    return new Tombstone({ id: new URL(row.object_id), formerType: Article, deleted: Temporal.Instant.from(new Date(row.changed_at).toISOString()) });
  });
  federation.setNodeInfoDispatcher('/nodeinfo/2.1', async () => ({
    software: {name:'content',version:'0.1.0'}, protocols:['activitypub'],
    usage: { users: {total:1,activeMonth:1,activeHalfyear:1}, localPosts:Number((await state.store.db.query("SELECT count(*) AS count FROM {{schema}}.content_objects WHERE state='public'"))[0]!.count),localComments:0 },
    openRegistrations:false
  }));
  const feedback=new FeedbackStore(state.store.db);
  const feedbackTarget=async(values:Record<'publication'|'id'|'kind',string>)=>{
    if(!state.config.feedbackEnabled||values.publication!==state.config.publicationId||!['replies','likes','shares'].includes(values.kind))return null;
    const target=state.config.origin+'/ap/objects/'+encodeURIComponent(values.publication)+'/'+encodeURIComponent(values.id);
    if(!(await state.store.object(target))?.payload)return null;
    return {target,kind:values.kind==='replies'?'reply' as const:values.kind==='likes'?'like' as const:'boost' as const};
  };
  federation.setOrderedCollectionDispatcher('feedback',ASObject,'/ap/objects/{publication}/{id}/{kind}',async(_ctx,values,cursor)=>{
    const scope=await feedbackTarget(values);if(!scope)return null;
    const offset=parseCursor(cursor),rows=await feedback.visible(scope.target,offset,21,scope.kind);
    const items=await Promise.all(rows.slice(0,20).map(row=>ASObject.fromJsonLd({
      '@context':'https://www.w3.org/ns/activitystreams',id:row.id,
      ...(row.kind==='reply'?{type:'Note',attributedTo:row.actor_id,inReplyTo:scope.target,content:row.html,updated:new Date(row.source_updated_at).toISOString(),to:[PUBLIC]}:{type:row.kind==='like'?'Like':'Announce',actor:row.actor_id,object:scope.target})
    })));
    return {items,nextCursor:rows.length>20?String(offset+20):null};
  }).setFirstCursor(async(_ctx,values)=>(await feedbackTarget(values))?'0':null)
    .setCounter(async(_ctx,values)=>{const scope=await feedbackTarget(values);return scope?feedback.countVisible(scope.target,scope.kind):null;});
  const receiveReply=async(ctx:InboxContext<FederationState>,activity:Create|Update)=>{
    if(!state.config.feedbackEnabled||!activity.id||!activity.actorId)return;
    const object=await activity.getObject(ctx);
    if(!(object instanceof Note)||!object.id||!object.replyTargetId||object.attributionIds.length!==1||object.attributionId?.href!==activity.actorId.href)return;
    if(![...object.toIds,...object.ccIds].some(id=>id.href===PUBLIC))return;
    if(activity instanceof Update&&!object.updated)return;
    await feedback.receive({id:object.id.href,activityId:activity.id.href,actor:activity.actorId.href,target:object.replyTargetId.href,kind:'reply',html:String(object.content??''),updatedAt:(object.updated??object.published??activity.published)?.toString()??new Date().toISOString()});
  };
  const receiveReaction=async(activity:Like|Announce)=>{
    if(!state.config.feedbackEnabled||!activity.id||!activity.actorId||!activity.objectId)return;
    if(activity instanceof Announce&&![...activity.toIds,...activity.ccIds].some(id=>id.href===PUBLIC))return;
    await feedback.receive({id:activity.id.href,activityId:activity.id.href,actor:activity.actorId.href,target:activity.objectId.href,kind:activity instanceof Like?'like':'boost',updatedAt:activity.published?.toString()??new Date().toISOString()});
  };
  federation.setInboxListeners(`${state.config.actorRoute}/inbox`, '/inbox')
    .on(Follow, async (ctx, follow) => {
      if (!follow.id || !follow.actorId || follow.objectId?.href !== state.identity.actorId) return;
      const actor = await follow.getActor(ctx);
      if (!actor?.id || !actor.inboxId || actor.id.href !== follow.actorId.href) throw new ServiceError(422,'invalid_follow_actor');
      await state.store.receiveFollow({ id:follow.id.href, actorId:follow.actorId.href, target:follow.objectId.href,
        actor:{ actorId:actor.id.href,inbox:actor.inboxId.href,sharedInbox:actor.endpoints?.sharedInbox?.href },
        follow: await follow.toJsonLd() as Record<string,unknown> });
    })
    .on(Create,receiveReply)
    .on(Update,receiveReply)
    .on(Like,async(_ctx,activity)=>{await receiveReaction(activity);})
    .on(Announce,async(_ctx,activity)=>{await receiveReaction(activity);})
    .on(Delete,async(_ctx,activity)=>{if(state.config.feedbackEnabled&&activity.actorId&&activity.objectId)await feedback.remove(activity.actorId.href,activity.objectId.href);})
    .on(Undo, async (ctx, undo) => {
      if (!undo.id || !undo.actorId || !undo.objectId) return;
      const follow = await undo.getObject(ctx);
      if(state.config.feedbackEnabled&&(follow instanceof Like||follow instanceof Announce)&&follow.id&&follow.actorId?.href===undo.actorId.href){await feedback.remove(undo.actorId.href,follow.id.href);return;}
      if (!(follow instanceof Follow) || !follow.id || !follow.actorId || !follow.objectId) return;
      await state.store.receiveUndoFollow({ id:undo.id.href,actorId:undo.actorId.href,followId:follow.id.href,
        followActorId:follow.actorId.href,target:follow.objectId.href });
    })
    .on(Activity, async (_ctx, activity) => {
      if (activity.id && activity.actorId) await state.store.recordUnsupported(activity.id.href,activity.actorId.href,activity.constructor.name);
    })
    .setSharedKeyDispatcher(() => ({ identifier:state.config.identifier }));
  federation.setOutboxPermanentFailureHandler(async (_ctx, failure) => {
    await state.store.db.query('INSERT INTO {{schema}}.delivery_failures(activity_id,error_code) VALUES($1,$2)',
      [failure.activity.id?.href??null,`permanent_${failure.statusCode}`]);
    // A failed remote inbox is not evidence that a user unfollowed. Preserve the relationship for operator review.
  });
  return federation;
}
function parseCursor(cursor: string|null): number {
  if (cursor == null) return 0;
  if (!/^\d{1,7}$/.test(cursor)) throw new ServiceError(400,'invalid_cursor');
  return Number(cursor);
}
export async function enqueueIntent(federation: Federation<FederationState>, state: FederationState, intent: Intent) {
  if (!state.config.federationEnabled) throw new Error('Federation delivery is disabled');
  const context = federation.createContext(new URL(state.config.origin),state);
  const activity = await Activity.fromJsonLd(intent.payload);
  await context.sendActivity({ privateKey:state.identity.privateKey,keyId:new URL(state.identity.keyId) },
    { id:new URL(intent.recipient_id),inboxId:new URL(intent.inbox),endpoints:{sharedInbox:intent.shared_inbox?new URL(intent.shared_inbox):null} },
    activity, { immediate:!!intent.history || (state.config.followHistoryLimit!>0 && intent.payload.type==='Accept'), orderingKey:activity.objectId?.href??state.identity.actorId,preferSharedInbox:false,fanout:'skip' });
}

function escapeHtml(value:string){return value.replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]!));}
