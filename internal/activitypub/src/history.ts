import {randomUUID} from 'node:crypto';
import {lockId} from './namespace.ts';
import type {Database} from './db.ts';
export type History={server:string;object_id:string;payload:Record<string,unknown>;interval_ms:number};
export type HistoryOptions={limit:number;intervalMs:number};
export async function scheduleFollowHistory(db:Database,recipient:{actorId:string;inbox:string},options:HistoryOptions){
 if(options.limit===0)return;
 if((await db.query('SELECT 1 FROM {{schema}}.feedback_blocks WHERE actor_id=$1',[recipient.actorId])).length)return;
 const server=new URL(recipient.actorId).origin;
 await db.query('SELECT pg_advisory_xact_lock($1::bigint)',[lockId('content',db.installation,db.schema,db.publication!,'follow-history',server)]);
 // Choose the bounded most recent public instances, then deliver oldest first.
 const rows=await db.query<{id:string;object_id:string;payload:Record<string,unknown>;object:Record<string,unknown>}>(`SELECT a.id,c.object_id,a.payload,c.payload AS object
  FROM {{schema}}.activities a JOIN {{schema}}.content_objects c ON c.publication_id=a.publication_id AND c.content_id=a.content_id
  AND c.object_id=a.payload->'object'->>'id'
  WHERE a.kind='Create' AND a.public AND c.state='public'
  ORDER BY a.created_at DESC,a.sequence DESC LIMIT $1`,[options.limit]);
 for(const [n,row] of rows.reverse().entries()){
  // Target the personal inbox, with no followers collection in the envelope or
  // embedded object's cc. Public visibility and canonical IDs/dates remain.
  const history:History={server,object_id:row.object_id,interval_ms:options.intervalMs,
   payload:{...row.payload,to:[recipient.actorId],cc:[],object:{...row.object,cc:[]}}};
  const delay=(n+1)*options.intervalMs;
  await db.query(`INSERT INTO {{schema}}.outbound_intents(id,activity_id,recipient_id,inbox,shared_inbox,history,available_at)
   VALUES($1,$2,$3,$4,NULL,$5,clock_timestamp()+$6*interval '1 millisecond') ON CONFLICT DO NOTHING`,
   [randomUUID(),row.id,recipient.actorId,recipient.inbox,history,delay]);
  // A cancelled unsent slot may resume on a NEW accepted Follow. Reuse its
  // unique slot/IDs; already completed or exhausted delivery never repeats.
  await db.query(`UPDATE {{schema}}.outbound_intents q SET recipient_id=$3,inbox=$4,shared_inbox=NULL,history=$5,
   state='pending',lease_token=NULL,lease_until=NULL,last_error=NULL,available_at=clock_timestamp()+$6*interval '1 millisecond'
   WHERE q.history->>'server'=$1 AND q.history->>'object_id'=$2 AND q.state='dead'
   AND q.last_error IN ('follow_undone','actor_blocked','accept_failed') AND q.attempts<10
   AND NOT EXISTS(SELECT 1 FROM {{schema}}.outbound_intents other WHERE other.activity_id=q.activity_id AND other.recipient_id=$3 AND other.id<>q.id)`,
   [server,row.object_id,recipient.actorId,recipient.inbox,history,delay]);
 }
}
