import type {Store,ContentRow} from './store.ts';
import {eventHash,ServiceError,type PublishingEvent,type ArchiveSnapshot} from './contracts.ts';
// A read-only query. One SQL statement gives a consistent receipt/current-object
// snapshot; no event replay, publishing, enrollment, locks or queue side effects.
export async function resolveReceipt(store:Store,event:PublishingEvent|ArchiveSnapshot,publisher:string){
 if(event.publication_id!==store.publication)throw new ServiceError(403,'publication_mismatch');
 if(event.source.id!==publisher)throw new ServiceError(403,'publisher_source_mismatch');
 const hash=eventHash(event);
 const [row]=await store.db.query<ContentRow&{payload_hash:string;result:'applied'|'stale'}>(`SELECT e.payload_hash,e.result,c.* FROM {{schema}}.publishing_events e
 JOIN {{schema}}.content_objects c ON c.publication_id=e.publication_id AND c.content_id=e.content_id WHERE e.event_id=$1`,[event.event_id]);
 if(!row)throw new ServiceError(404,'receipt_not_found');
 if(row.payload_hash!==hash)throw new ServiceError(409,'event_id_conflict');
 const same=row.source_id===event.source.id&&String(row.source_epoch)===event.source.epoch&&row.instance_id===event.object_instance&&BigInt(row.revision)>=BigInt(event.revision);
 const reason=row.result==='stale'?'stale_receipt':!same?'superseded':row.state!=='public'?'not_public':'confirmed';
 return {schema_version:1,event_id:event.event_id,publication_id:event.publication_id,content_id:event.content_id,source:event.source,revision:event.revision,event_hash:hash,result:row.result,
  projection:reason==='confirmed'?{object_id:row.object_id,object_instance:row.instance_id,revision:String(row.revision),state:'public'}:null,reason};
}
