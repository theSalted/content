import {z} from 'zod';
import {remoteUrl,sanitize,ServiceError} from './contracts.ts';
import type {Database,Row} from './db.ts';
import {lockId} from './namespace.ts';
export const moderation=z.strictObject({id:z.url().max(2048),decision:z.enum(['approved','rejected']),expected_activity_id:z.url().max(2048)});
export const actorBlock=z.strictObject({actor:z.url().max(2048),blocked:z.boolean()});
type Kind='reply'|'like'|'boost';
type Incoming={id:string;activityId:string;actor:string;target:string;kind:Kind;html?:string;updatedAt:string};
type FeedbackRow=Row&{id:string;actor_id:string;target:string;kind:Kind;html:string;state:string;source_updated_at:string};
function owned(id:string,actor:string){
 if(id.length>2048||actor.length>2048)throw new ServiceError(422,'feedback_identifier_too_long');
 remoteUrl(id);remoteUrl(actor);if(new URL(id).origin!==new URL(actor).origin)throw new ServiceError(422,'feedback_owner_mismatch');
}
export class FeedbackStore {
 readonly db:Database;
 constructor(db:Database){if(!db.publication)throw new Error('Publication scope required');this.db=db;}
 async receive(input:Incoming){
  owned(input.id,input.actor);owned(input.activityId,input.actor);
  if(Buffer.byteLength(input.html??'')>16384||!Number.isFinite(Date.parse(input.updatedAt)))throw new ServiceError(422,'invalid_feedback');
  return this.db.transaction(async tx=>{
   await tx.query('SELECT pg_advisory_xact_lock($1::bigint)',[lockId('content',tx.installation,tx.schema,tx.publication!,'feedback',input.actor)]);
   await tx.query('SELECT pg_advisory_xact_lock($1::bigint)',[lockId('content',tx.installation,tx.schema,tx.publication!,'feedback-id',input.id)]);
   if((await tx.query('SELECT 1 FROM {{schema}}.feedback_blocks WHERE actor_id=$1',[input.actor])).length)return 'blocked';
   if((await tx.query('SELECT 1 FROM {{schema}}.feedback_tombstones WHERE id=$1 AND actor_id=$2',[input.id,input.actor])).length)return 'deleted';
   if(!(await tx.query("SELECT 1 FROM {{schema}}.content_objects WHERE object_id=$1 AND state='public'",[input.target])).length)return 'not_public_target';
   const [old]=await tx.query<FeedbackRow>('SELECT * FROM {{schema}}.feedback WHERE id=$1 FOR UPDATE',[input.id]);
   if(old&&(old.actor_id!==input.actor||old.target!==input.target||old.kind!==input.kind))throw new ServiceError(422,'feedback_owner_mismatch');
   if(old?.state==='deleted')return 'deleted';
   if(old&&Date.parse(input.updatedAt)<=new Date(old.source_updated_at).getTime())return 'stale';
   if(!old||old.state!=='pending'){await tx.query('SELECT pg_advisory_xact_lock($1::bigint)',[lockId('content',tx.installation,tx.schema,tx.publication!,'feedback-cap')]);const [counts]=await tx.query<{actor_count:string;total:string}>("SELECT count(*) FILTER(WHERE actor_id=$1) AS actor_count,count(*) AS total FROM {{schema}}.feedback WHERE state='pending'",[input.actor]);if(Number(counts?.actor_count)>=20||Number(counts?.total)>=1000)return 'limited';}
   if(!(await tx.query('INSERT INTO {{schema}}.inbox_receipts(id,actor_id,kind) VALUES($1,$2,$3) ON CONFLICT DO NOTHING RETURNING id',[input.activityId,input.actor,'feedback_'+input.kind])).length)return 'duplicate';
   await tx.query(`INSERT INTO {{schema}}.feedback(id,activity_id,actor_id,target,kind,html,source_updated_at) VALUES($1,$2,$3,$4,$5,$6,$7)
    ON CONFLICT(publication_id,id) DO UPDATE SET activity_id=EXCLUDED.activity_id,html=EXCLUDED.html,source_updated_at=EXCLUDED.source_updated_at,state='pending',moderated_at=NULL`,
    [input.id,input.activityId,input.actor,input.target,input.kind,sanitize(input.html??''),input.updatedAt]);return 'pending';
  });
 }
 async remove(actor:string,id:string){
  owned(id,actor);
  return this.db.transaction(async tx=>{
   await tx.query('SELECT pg_advisory_xact_lock($1::bigint)',[lockId('content',tx.installation,tx.schema,tx.publication!,'feedback',actor)]);
   await tx.query('SELECT pg_advisory_xact_lock($1::bigint)',[lockId('content',tx.installation,tx.schema,tx.publication!,'feedback-id',id)]);
   const [old]=await tx.query<FeedbackRow>('SELECT * FROM {{schema}}.feedback WHERE id=$1 FOR UPDATE',[id]);
   if(old&&old.actor_id!==actor)throw new ServiceError(422,'feedback_owner_mismatch');
   // Preserve bounded out-of-order removals. Known comments can always be removed.
   await tx.query('SELECT pg_advisory_xact_lock($1::bigint)',[lockId('content',tx.installation,tx.schema,tx.publication!,'feedback-tombstone-cap')]);
   const [count]=await tx.query<{actor_count:string;total:string}>('SELECT count(*) FILTER(WHERE actor_id=$1) AS actor_count,count(*) AS total FROM {{schema}}.feedback_tombstones',[actor]);
   if(old||Number(count?.actor_count)<100&&Number(count?.total)<10000)await tx.query('INSERT INTO {{schema}}.feedback_tombstones(id,actor_id) VALUES($1,$2) ON CONFLICT DO NOTHING',[id,actor]);
   await tx.query("UPDATE {{schema}}.feedback SET state='deleted',html='' WHERE id=$1 AND actor_id=$2",[id,actor]);
  });
 }
 async list(state='pending',offset=0,limit=100){
  if(!Number.isInteger(offset)||offset<0||offset>9999999||!Number.isInteger(limit)||limit<1||limit>101)throw new ServiceError(400,'invalid_feedback_page');
  if(!['pending','approved','rejected','deleted'].includes(state))throw new ServiceError(400,'invalid_feedback_state');
  return this.db.query<FeedbackRow>('SELECT id,activity_id,actor_id,target,kind,html,state,source_updated_at,received_at FROM {{schema}}.feedback WHERE state=$1 ORDER BY received_at DESC,id LIMIT $2 OFFSET $3',[state,limit,offset]);
 }
 async moderate(id:string,decision:'approved'|'rejected',expectedActivityId:string){
  return this.db.transaction(async tx=>{
   const [initial]=await tx.query<{actor_id:string}>('SELECT actor_id FROM {{schema}}.feedback WHERE id=$1',[id]);
   if(!initial)throw new ServiceError(404,'feedback_not_found');
   await tx.query('SELECT pg_advisory_xact_lock($1::bigint)',[lockId('content',tx.installation,tx.schema,tx.publication!,'feedback',initial.actor_id)]);
   const [row]=await tx.query<FeedbackRow>('SELECT * FROM {{schema}}.feedback WHERE id=$1 FOR UPDATE',[id]);
   if(!row||row.state==='deleted')throw new ServiceError(404,'feedback_not_found');
   if(row.activity_id!==expectedActivityId)throw new ServiceError(409,'feedback_changed');
   if(decision==='approved'&&(await tx.query('SELECT 1 FROM {{schema}}.feedback_blocks WHERE actor_id=$1',[row.actor_id])).length)throw new ServiceError(404,'feedback_not_found');
   await tx.query("UPDATE {{schema}}.feedback SET state=$2,moderated_at=now() WHERE id=$1",[id,decision]);
  });
 }
 async block(actor:string,blocked:boolean){
  remoteUrl(actor);return this.db.transaction(async tx=>{
   await tx.query('SELECT pg_advisory_xact_lock($1::bigint)',[lockId('content',tx.installation,tx.schema,tx.publication!,'feedback',actor)]);
   if(blocked){await tx.query('INSERT INTO {{schema}}.feedback_blocks(actor_id) VALUES($1) ON CONFLICT DO NOTHING',[actor]);await tx.query("UPDATE {{schema}}.outbound_intents SET state='dead',lease_token=NULL,lease_until=NULL,last_error='actor_blocked' WHERE recipient_id=$1 AND history IS NOT NULL AND state IN ('pending','leased')",[actor]);await tx.query("UPDATE {{schema}}.feedback SET state='rejected',moderated_at=now() WHERE actor_id=$1 AND state<>'deleted'",[actor]);}
   else await tx.query('DELETE FROM {{schema}}.feedback_blocks WHERE actor_id=$1',[actor]);
  });
 }
 async countVisible(target:string,kind:'reply'|'like'|'boost'){
  const [row]=await this.db.query<{count:string}>("SELECT count(*) AS count FROM {{schema}}.feedback f JOIN {{schema}}.content_objects c ON c.object_id=f.target AND c.publication_id=f.publication_id WHERE f.target=$1 AND f.kind=$2 AND f.state='approved' AND c.state='public' AND NOT EXISTS(SELECT 1 FROM {{schema}}.feedback_blocks b WHERE b.actor_id=f.actor_id)",[target,kind]);return Number(row?.count??0);
 }
 async visible(target:string,offset=0,limit=100,kind?:'reply'|'like'|'boost'){
  return this.db.query<FeedbackRow>(`SELECT f.id,f.actor_id,f.target,f.kind,f.html,f.source_updated_at FROM {{schema}}.feedback f
   JOIN {{schema}}.content_objects c ON c.object_id=f.target AND c.publication_id=f.publication_id
   WHERE f.target=$1 AND f.state='approved' AND c.state='public' AND ($4::text IS NULL OR f.kind=$4) AND NOT EXISTS(SELECT 1 FROM {{schema}}.feedback_blocks b WHERE b.actor_id=f.actor_id) ORDER BY f.received_at,f.id LIMIT $2 OFFSET $3`,[target,Math.min(limit,100),offset,kind??null]);
 }
}

/** Fail closed before accepting inbox work when moderation storage is enabled. */
export async function validateFeedbackStorage(db:Database){
 const rows=await db.query<{relname:string;safe:boolean}>(`SELECT c.relname,c.relrowsecurity AND c.relforcerowsecurity
 AND has_table_privilege(current_user,c.oid,'SELECT') AND has_table_privilege(current_user,c.oid,'INSERT')
 AND (c.relname<>'feedback' OR has_table_privilege(current_user,c.oid,'UPDATE'))
 AND (c.relname<>'feedback_blocks' OR has_table_privilege(current_user,c.oid,'DELETE')) AS safe
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=$1 AND c.relname IN ('feedback','feedback_tombstones','feedback_blocks') AND c.relkind='r'`,[db.schema]);
 if(rows.length!==3||rows.some(row=>!row.safe))throw new Error('Feedback requires migrated FORCE RLS tables and exact runtime privileges');
}
