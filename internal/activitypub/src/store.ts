import {projectArticleMedia,type PublicMediaMapping} from './media-origins.ts';
import {scheduleFollowHistory,type History,type HistoryOptions} from './history.ts';
import {lockId} from './namespace.ts';
import { randomUUID } from 'node:crypto';
import {articlePresentation,authoredTags} from './article-presentation.ts';
import { nextUpdatedAt } from './projection-time.ts';
import type { Database, Row } from './db.ts';
import { eventHash, remoteUrl, sanitize, ServiceError } from './contracts.ts';
import type { PublishingEvent, ArchiveSnapshot } from './contracts.ts';

export const PUBLIC = 'https://www.w3.org/ns/activitystreams#Public';
const CONTEXT = 'https://www.w3.org/ns/activitystreams';
export type ContentRow = Row & {
  content_id: string; object_id: string; instance_id: string; source_id: string; source_epoch: string; revision: string;
  state: 'unpublished'|'public'|'withdrawn'|'deleted'; payload: Record<string, unknown>|null;
  published_at: string|null; changed_at: string;
};
export type Recipient = { actorId: string; inbox: string; sharedInbox?: string|null };
export type Intent = Row & {
  id: string; activity_id: string; recipient_id: string; inbox: string; shared_inbox: string|null;
  history:History|null; attempts: number; lease_token: string; payload: Record<string, unknown>;
};
export class Store {
  readonly db: Database;
  readonly actorId: string;
  readonly origin: string;
  readonly publication: string;
  readonly contentOrigins:readonly string[];
  readonly history:HistoryOptions;
  readonly publicMedia?:PublicMediaMapping;
  constructor(db: Database, actorId: string, contentOrigins:readonly string[]=[new URL(actorId).origin],history:HistoryOptions={limit:0,intervalMs:2000},publicMedia?:PublicMediaMapping) { this.publicMedia=publicMedia; if(!Number.isInteger(history.limit)||history.limit<0||history.limit>100||!Number.isInteger(history.intervalMs)||history.intervalMs<1000||history.intervalMs>60000)throw new Error('Invalid follow history limits'); this.history=history; this.contentOrigins=contentOrigins; if(!db.publication)throw new Error('Publication-scoped database required'); this.publication=db.publication; this.db = db; this.actorId = actorId; this.origin = new URL(actorId).origin; }

  private async activity(tx: Database, kind: string, payload: Record<string, unknown>, contentId: string|null,
    recipients: Recipient[], isPublic: boolean, historicalDate:string|null=null): Promise<string> {
    const id = `${this.origin}/ap/activities/${randomUUID()}`;
    const activity = { '@context': CONTEXT, id, type: kind, actor: this.actorId, ...payload };
    await tx.query('INSERT INTO {{schema}}.activities(id,content_id,kind,payload,public,created_at) VALUES($1,$2,$3,$4,$5,COALESCE($6::timestamptz,now()))',
      [id, contentId, kind, activity, isPublic,historicalDate]);
    for (const recipient of recipients) {
      await tx.query(`INSERT INTO {{schema}}.outbound_intents(id,activity_id,recipient_id,inbox,shared_inbox)
        VALUES($1,$2,$3,$4,$5) ON CONFLICT(publication_id,activity_id,recipient_id) DO NOTHING`,
      [randomUUID(), id, recipient.actorId, recipient.inbox, recipient.sharedInbox ?? null]);
    }
    return id;
  }

  private async reserveObjectId(tx:Database,objectId:string,contentId:string) {
    // The global unique key serializes concurrent claims across publications
    // and current/retired representations. A conflict never releases an ID.
    const claimed=await tx.query(`INSERT INTO {{schema}}.object_id_reservations(object_id,content_id)
      VALUES($1,$2) ON CONFLICT(object_id) DO NOTHING RETURNING object_id`,[objectId,contentId]);
    if(!claimed.length)throw new ServiceError(409,'object_instance_reused');
  }

  // Normal runtime always addresses the publication's followers. A controlled
  // fixture can override this method without an environment/config switch.
  protected async publicationRecipients(tx:Database,_event:PublishingEvent|ArchiveSnapshot):Promise<Recipient[]>{
    return (await tx.query<{actor_id:string;inbox:string;shared_inbox:string|null}>('SELECT actor_id,inbox,shared_inbox FROM {{schema}}.followers'))
      .map(f=>({actorId:f.actor_id,inbox:f.inbox,sharedInbox:f.shared_inbox}));
  }

  async publish(event: PublishingEvent, publisherId: string) {
    return this.apply(event,publisherId,false);
  }
  async archive(event: ArchiveSnapshot, publisherId: string) {
    return this.apply(event,publisherId,true);
  }
  private async apply(event: PublishingEvent|ArchiveSnapshot, publisherId:string, archive:boolean) {
    if(event.publication_id!==this.publication)throw new ServiceError(403,'publication_mismatch');
    if (event.source.id !== publisherId) throw new ServiceError(403, 'publisher_source_mismatch');
    if ('document' in event) {
      const url = new URL(event.document.url);
      if (!this.contentOrigins.includes(url.origin) || url.hash || url.search) throw new ServiceError(422, 'noncanonical_article_url');
      if(event.document.tags?.some(tag=>!this.contentOrigins.includes(new URL(tag.href).origin)))throw new ServiceError(422,'noncanonical_tag_url');
    }
    return this.db.transaction(async tx => {
      await tx.query('SELECT pg_advisory_xact_lock($1::bigint)', [lockId('content',tx.installation,'activitypub',tx.schema,this.publication,'event',event.event_id)]);
      await tx.query('SELECT pg_advisory_xact_lock($1::bigint)', [lockId('content',tx.installation,'activitypub',tx.schema,this.publication,'content',event.content_id)]);
      const hash = eventHash(event);
      const duplicate = (await tx.query<{ payload_hash: string; result: string }>(
        'SELECT payload_hash,result FROM {{schema}}.publishing_events WHERE event_id=$1', [event.event_id]))[0];
      if (duplicate) {
        if (duplicate.payload_hash !== hash) throw new ServiceError(409, 'event_id_conflict');
        return { result: 'duplicate', original_result: duplicate.result };
      }
      let content = (await tx.query<ContentRow>('SELECT * FROM {{schema}}.content_objects WHERE content_id=$1 FOR UPDATE', [event.content_id]))[0];
      if(archive&&content)throw new ServiceError(409,'archive_content_exists');
      if (!content) {
        if (event.source.epoch !== '1') throw new ServiceError(409, 'unknown_authority_generation');
        if (event.type === 'content.updated') throw new ServiceError(409, 'publish_required');
        const objectId=`${this.origin}/ap/objects/${encodeURIComponent(this.publication)}/${event.object_instance}`;
        await this.reserveObjectId(tx,objectId,event.content_id);
        await tx.query(`INSERT INTO {{schema}}.content_objects(content_id,object_id,instance_id,source_id,source_epoch,state)
          VALUES($1,$2,$3,$4,1,'unpublished')`, [event.content_id,objectId,event.object_instance,event.source.id]);
        content = (await tx.query<ContentRow>('SELECT * FROM {{schema}}.content_objects WHERE content_id=$1', [event.content_id]))[0]!;
      }
      if (content.source_id !== event.source.id || String(content.source_epoch) !== event.source.epoch)
        throw new ServiceError(409, 'source_not_authoritative');
      const revision = BigInt(content.revision);
      if (BigInt(event.revision) === revision) throw new ServiceError(409, 'revision_conflict');
      const stale = BigInt(event.revision) < revision;
      if (!stale) {
        const publishing = 'document' in event;
        if(content.state==='deleted'&&event.type!=='content.deleted')throw new ServiceError(409,'tombstone_is_final');
        if(publishing&&content.state==='withdrawn'){
          if(event.type!=='content.published'||event.object_instance===content.instance_id)throw new ServiceError(409,'tombstone_is_final');
          const objectId=`${this.origin}/ap/objects/${encodeURIComponent(this.publication)}/${event.object_instance}`;
          await this.reserveObjectId(tx,objectId,event.content_id);
          await tx.query('INSERT INTO {{schema}}.retired_objects(object_id,published_at,changed_at) VALUES($1,$2,$3) ON CONFLICT DO NOTHING',[content.object_id,content.published_at,content.changed_at]);
          await tx.query("UPDATE {{schema}}.content_objects SET object_id=$2,instance_id=$3,state='unpublished',published_at=NULL WHERE content_id=$1",[event.content_id,objectId,event.object_instance]);
          content={...content,object_id:objectId,instance_id:event.object_instance,state:'unpublished',published_at:null};
        }
        if(event.object_instance!==content.instance_id)throw new ServiceError(409,'object_instance_mismatch');
        if (event.type === 'content.updated' && content.state !== 'public') throw new ServiceError(409, 'publish_required');
        const followers = archive ? [] : await this.publicationRecipients(tx,event);
        if (publishing) {
          const doc = event.document;
          const {html,attachments}=articlePresentation(doc.html,doc.url);
          const oldPublished = content.payload?.published;
          const object = projectArticleMedia({ '@context': doc.tags?.length?[CONTEXT,{Hashtag:'https://www.w3.org/ns/activitystreams#Hashtag'}]:CONTEXT, id: content.object_id, type: 'Article',
            attributedTo: this.actorId, name: doc.title, content: html,
            ...(attachments.length?{attachment:attachments}:{}),
            ...(doc.tags?.length?{tag:authoredTags(doc.tags)}:{}),
            ...(doc.language ? { contentMap: { [doc.language]: html } } : {}),
            url: doc.url, published: oldPublished ?? doc.published_at,
            updated: nextUpdatedAt(event.occurred_at,content.payload?.updated),
            to: [PUBLIC], cc: [`${this.actorId}/followers`] },this.publicMedia);
          await tx.query(`UPDATE {{schema}}.content_objects SET revision=$2,state='public',payload=$3,
            published_at=COALESCE(published_at,$4),changed_at=$5 WHERE content_id=$1`,
          [event.content_id, event.revision, object, doc.published_at, event.occurred_at]);
          await this.activity(tx, content.state === 'public' ? 'Update' : 'Create',
            { object, to: [PUBLIC], cc: [`${this.actorId}/followers`], published: archive?doc.published_at:event.occurred_at }, event.content_id, followers, true,archive?doc.published_at:null);
        } else {
          if (content.state === 'public') {
            await this.activity(tx, 'Delete', { object: content.object_id, to: [PUBLIC], cc: [`${this.actorId}/followers`] },
              event.content_id, followers, true);
            // Public history must not expose old article HTML after withdrawal. Keep queued payloads until replaced below.
            await tx.query(`UPDATE {{schema}}.activities SET public=false WHERE content_id=$1 AND kind IN ('Create','Update')`, [event.content_id]);
            // Cancel activities not yet handed to Fedify. Already handed-off jobs may still arrive; a later Delete follows.
            await tx.query(`UPDATE {{schema}}.outbound_intents SET state='dead',lease_token=NULL,lease_until=NULL,last_error='content_withdrawn'
              WHERE activity_id IN (SELECT id FROM {{schema}}.activities WHERE content_id=$1 AND kind IN ('Create','Update'))
              AND state IN ('pending','leased')`, [event.content_id]);
          }
          await tx.query(`UPDATE {{schema}}.content_objects SET revision=$2,state=$3,payload=NULL,changed_at=$4 WHERE content_id=$1`,
            [event.content_id, event.revision, event.type === 'content.deleted' ? 'deleted' : 'withdrawn', event.occurred_at]);
        }
      }
      await tx.query(`INSERT INTO {{schema}}.publishing_events(event_id,content_id,payload_hash,source_id,source_epoch,revision,result)
        VALUES($1,$2,$3,$4,$5,$6,$7)`, [event.event_id,event.content_id,hash,event.source.id,event.source.epoch,event.revision,stale?'stale':'applied']);
      return { result: stale ? 'stale' : 'applied', object_id: content.object_id };
    });
  }

  // Operator-only migration primitive; intentionally not exposed over HTTP.
  async transferAuthority(contentId: string, expectedSource: string, expectedEpoch: string, newSource: string) {
    return this.db.transaction(async tx => {
      await tx.query('SELECT pg_advisory_xact_lock($1::bigint)', [lockId('content',tx.installation,'activitypub',tx.schema,this.publication,'content',contentId)]);
      const result = await tx.query(`UPDATE {{schema}}.content_objects SET source_id=$4,source_epoch=source_epoch+1
        WHERE content_id=$1 AND source_id=$2 AND source_epoch=$3 RETURNING source_epoch,revision,object_id`,
      [contentId,expectedSource,expectedEpoch,newSource]);
      if (!result.length) throw new ServiceError(409, 'authority_transfer_conflict');
      return result[0]!;
    });
  }

  async receiveFollow(input: { id: string; actorId: string; target: string; actor: Recipient; follow: Record<string,unknown> }) {
    if (input.target !== this.actorId || input.actor.actorId !== input.actorId) throw new ServiceError(422, 'follow_actor_mismatch');
    remoteUrl(input.actorId); remoteUrl(input.actor.inbox);
    ownedActivity(input.id,input.actorId);
    if (input.actor.sharedInbox) remoteUrl(input.actor.sharedInbox);
    return this.db.transaction(async tx => {
      await tx.query('SELECT pg_advisory_xact_lock($1::bigint)', [lockId('content',tx.installation,'activitypub',tx.schema,this.publication,'follower',input.actorId)]);
      if (!(await tx.query(`INSERT INTO {{schema}}.inbox_receipts(id,actor_id,kind) VALUES($1,$2,'Follow') ON CONFLICT DO NOTHING RETURNING id`, [input.id,input.actorId])).length) return;
      const undone = await tx.query('SELECT 1 FROM {{schema}}.undone_follows WHERE follow_id=$1 AND actor_id=$2', [input.id,input.actorId]);
      if (undone.length) return;
      await tx.query(`INSERT INTO {{schema}}.followers(actor_id,inbox,shared_inbox,follow_id) VALUES($1,$2,$3,$4)
        ON CONFLICT(publication_id,actor_id) DO UPDATE SET inbox=EXCLUDED.inbox,shared_inbox=EXCLUDED.shared_inbox,follow_id=EXCLUDED.follow_id`,
      [input.actorId,input.actor.inbox,input.actor.sharedInbox??null,input.id]);
      await this.activity(tx, 'Accept', { object: input.follow, to: [input.actorId] }, null, [input.actor], false);
      await scheduleFollowHistory(tx,input.actor,this.history);
    });
  }
  async receiveUndoFollow(input: { id: string; actorId: string; followId: string; followActorId: string; target: string }) {
    if (input.target !== this.actorId || input.actorId !== input.followActorId) throw new ServiceError(422,'undo_actor_mismatch');
    ownedActivity(input.id,input.actorId); ownedActivity(input.followId,input.actorId);
    return this.db.transaction(async tx => {
      await tx.query('SELECT pg_advisory_xact_lock($1::bigint)', [lockId('content',tx.installation,'activitypub',tx.schema,this.publication,'follower',input.actorId)]);
      if (!(await tx.query(`INSERT INTO {{schema}}.inbox_receipts(id,actor_id,kind) VALUES($1,$2,'Undo') ON CONFLICT DO NOTHING RETURNING id`, [input.id,input.actorId])).length) return;
      await tx.query('INSERT INTO {{schema}}.undone_follows(follow_id,actor_id) VALUES($1,$2) ON CONFLICT DO NOTHING', [input.followId,input.actorId]);
      // A verified legacy import may lack the original Follow ID. A signed Undo
      // of that actor's Follow may remove it; it cannot remove a newer known Follow.
      await tx.query('DELETE FROM {{schema}}.followers WHERE actor_id=$1 AND (follow_id=$2 OR follow_id IS NULL)', [input.actorId,input.followId]);
      // Do not deliver a queued Accept for a relationship which has since been withdrawn.
      await tx.query(`UPDATE {{schema}}.outbound_intents SET state='dead',lease_token=NULL,lease_until=NULL,last_error='follow_undone'
        WHERE recipient_id=$1 AND state IN ('pending','leased') AND activity_id IN
        (SELECT id FROM {{schema}}.activities WHERE kind='Accept' AND payload->'object'->>'id'=$2)`, [input.actorId,input.followId]);
      await tx.query("UPDATE {{schema}}.outbound_intents SET state='dead',lease_token=NULL,lease_until=NULL,last_error='follow_undone' WHERE recipient_id=$1 AND history IS NOT NULL AND state IN ('pending','leased') AND NOT EXISTS(SELECT 1 FROM {{schema}}.followers WHERE actor_id=$1)",[input.actorId]);
    });
  }
  async recordUnsupported(id: string, actorId: string, kind: string) {
    ownedActivity(id,actorId);
    await this.db.query('INSERT INTO {{schema}}.inbox_receipts(id,actor_id,kind) VALUES($1,$2,$3) ON CONFLICT DO NOTHING', [id,actorId,`unsupported:${kind}`]);
  }
  async object(id:string){
    const [current]=await this.db.query<ContentRow>('SELECT * FROM {{schema}}.content_objects WHERE object_id=$1',[id]);
    if(current)return current;
    const [old]=await this.db.query<ContentRow>("SELECT object_id,published_at,changed_at,NULL::jsonb AS payload FROM {{schema}}.retired_objects WHERE object_id=$1",[id]);
    return old;
  }
  async outbox(offset: number, limit = 20) {
    return this.db.query<{ payload: Record<string,unknown> }>(
      `SELECT jsonb_set(a.payload,'{object}',c.payload) AS payload ${this.publicOutbox()}
       ORDER BY a.created_at DESC,a.sequence DESC LIMIT $1 OFFSET $2`, [limit,offset]);
  }
  async outboxCount() {
    return Number((await this.db.query<{count:string}>(`SELECT count(*) AS count ${this.publicOutbox()}`))[0]!.count);
  }
  private publicOutbox() {
    // A profile is a collection of current published articles, not the delivery
    // log. Keep the original Create ID/date, resolve its current object, and
    // retain immutable Update/Delete activities separately for delivery/audit.
    return `FROM {{schema}}.activities a JOIN {{schema}}.content_objects c
      ON c.publication_id=a.publication_id AND c.content_id=a.content_id
      AND c.object_id=a.payload->'object'->>'id'
      WHERE a.public AND a.kind='Create' AND c.state='public' AND c.payload IS NOT NULL`;
  }
  async followers(offset: number, limit = 20) {
    return this.db.query<{ actor_id: string; inbox: string; shared_inbox: string|null }>(
      'SELECT actor_id,inbox,shared_inbox FROM {{schema}}.followers ORDER BY actor_id LIMIT $1 OFFSET $2', [limit,offset]);
  }
  async claimIntent(now = new Date()): Promise<Intent|undefined> {
    return this.db.transaction(async tx => {
      const rows = await tx.query<{ id: string }>(`SELECT q.id FROM {{schema}}.outbound_intents q
        WHERE ((q.state='pending' AND q.available_at <= $1) OR (q.state='leased' AND q.lease_until <= $1))
        AND (q.history IS NULL OR $2::boolean)
        AND (q.history IS NULL OR EXISTS(SELECT 1 FROM {{schema}}.followers f JOIN {{schema}}.activities a ON a.kind='Accept' AND a.payload->'object'->>'id'=f.follow_id JOIN {{schema}}.outbound_intents accepted ON accepted.activity_id=a.id AND accepted.recipient_id=f.actor_id AND accepted.state='enqueued' WHERE f.actor_id=q.recipient_id))
        AND NOT EXISTS(SELECT 1 FROM {{schema}}.outbound_intents earlier WHERE q.history IS NOT NULL AND earlier.history->>'server'=q.history->>'server' AND earlier.sequence<q.sequence AND earlier.state IN ('pending','leased'))
        AND NOT EXISTS (SELECT 1 FROM {{schema}}.outbound_intents prior JOIN {{schema}}.activities pa ON pa.id=prior.activity_id
          JOIN {{schema}}.activities ca ON ca.id=q.activity_id
          WHERE prior.recipient_id=q.recipient_id AND prior.id<>q.id AND prior.state IN ('pending','leased')
            AND ca.content_id IS NOT NULL AND pa.content_id=ca.content_id
            AND prior.sequence<q.sequence)
        ORDER BY (q.history IS NOT NULL),q.sequence FOR UPDATE OF q SKIP LOCKED LIMIT 1`, [now,this.history.limit>0]);
      if (!rows[0]) return;
      const token = randomUUID();
      await tx.query(`UPDATE {{schema}}.outbound_intents SET state='leased',lease_token=$2,lease_until=$3,attempts=attempts+1 WHERE id=$1`,
        [rows[0].id,token,new Date(now.getTime()+60_000)]);
      return (await tx.query<Intent>(`SELECT q.*,COALESCE(q.history->'payload',a.payload) AS payload FROM {{schema}}.outbound_intents q JOIN {{schema}}.activities a ON a.id=q.activity_id WHERE q.id=$1`, [rows[0].id]))[0];
    });
  }
  async handoffIntent(job: Intent, enqueue: (intent:Intent)=>Promise<void>) {
    return this.db.transaction(async tx=>{
      const current=await tx.query(`SELECT id FROM {{schema}}.outbound_intents WHERE id=$1 AND state='leased' AND lease_token=$2 FOR UPDATE`,[job.id,job.lease_token]);
      if(!current.length)return false;
      if(job.history){
        const [allowed]=await tx.query(`SELECT 1 FROM {{schema}}.followers f JOIN {{schema}}.content_objects c ON c.object_id=$2
          WHERE f.actor_id=$1 AND c.state='public' AND NOT EXISTS(SELECT 1 FROM {{schema}}.feedback_blocks b WHERE b.actor_id=f.actor_id) FOR SHARE OF f,c`,[job.recipient_id,job.history.object_id]);
        if(!allowed){await tx.query("UPDATE {{schema}}.outbound_intents SET state='dead',lease_token=NULL,lease_until=NULL,last_error='history_not_public_or_following' WHERE id=$1",[job.id]);return false;}
      }
      // Hold the row lock through the durable transport handoff so withdrawal/reclaim cannot overtake it.
      await enqueue(job);
      await tx.query("UPDATE {{schema}}.outbound_intents SET state='enqueued',lease_until=NULL,lease_token=NULL,last_error=NULL WHERE id=$1",[job.id]);
      if(job.history)await tx.query(`UPDATE {{schema}}.outbound_intents SET available_at=GREATEST(available_at,clock_timestamp()+$2*interval '1 millisecond')
        WHERE history->>'server'=$1 AND state='pending'`,[job.history.server,job.history.interval_ms]);
      return true;
    });
  }
  async failIntent(job: Intent, now = new Date()) {
    const delay = Math.min(3_600_000, 1000 * 2 ** Math.min(job.attempts,12));
    await this.db.transaction(async tx=>{
      const changed=await tx.query(`UPDATE {{schema}}.outbound_intents SET state=$3,available_at=$4,lease_until=NULL,lease_token=NULL,last_error='enqueue_failed'
        WHERE id=$1 AND state='leased' AND lease_token=$2 RETURNING id`, [job.id,job.lease_token,job.attempts>=10?'dead':'pending',new Date(now.getTime()+delay)]);
      if(changed.length&&job.attempts>=10&&job.payload.type==='Accept')await tx.query(`UPDATE {{schema}}.outbound_intents SET state='dead',lease_token=NULL,lease_until=NULL,last_error='accept_failed'
        WHERE recipient_id=$1 AND history IS NOT NULL AND state IN ('pending','leased')
        AND EXISTS(SELECT 1 FROM {{schema}}.followers WHERE actor_id=$1 AND follow_id=$2)`,[job.recipient_id,(job.payload.object as {id:string}).id]);
    });
  }
  async heartbeat() { await this.db.query(`INSERT INTO {{schema}}.worker_heartbeats(name) VALUES('federation') ON CONFLICT(publication_id,name) DO UPDATE SET updated_at=now()`); }
}
function ownedActivity(id:string,actorId:string) {
  remoteUrl(id);remoteUrl(actorId);
  if(new URL(id).origin!==new URL(actorId).origin)throw new ServiceError(422,'activity_origin_mismatch');
}
