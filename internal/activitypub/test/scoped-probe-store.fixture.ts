// Explicit test packaging only. The runtime image does not include test/.
// No environment flag can activate recipient fencing in the normal service.
import {Store,type Recipient} from '../src/store.ts';
import type {Database} from '../src/db.ts';
import {ServiceError,type PublishingEvent,type ArchiveSnapshot} from '../src/contracts.ts';
export class ScopedProbeStore extends Store {
 readonly contentId:string;readonly peerActor:string;readonly publisher:string;
 constructor(db:Database,actor:string,contentId:string,peerActor:string,publisher:string,origins:readonly string[]){
  super(db,actor,origins);this.contentId=contentId;this.peerActor=peerActor;this.publisher=publisher;
 }
 override async publish(event:PublishingEvent,publisher:string){
  if(event.content_id!==this.contentId||publisher!==this.publisher)throw new ServiceError(403,'probe_scope_mismatch');
  return super.publish(event,publisher);
 }
 override async archive(_event:ArchiveSnapshot,_publisher:string):Promise<never>{throw new ServiceError(403,'probe_scope_mismatch');}
 protected override async publicationRecipients(tx:Database,event:PublishingEvent|ArchiveSnapshot):Promise<Recipient[]>{
  const recipients=(await super.publicationRecipients(tx,event)).filter(f=>f.actorId===this.peerActor);
  if(event.type==='content.published'&&recipients.length!==1)throw new ServiceError(409,'probe_peer_not_following');
  return recipients;
 }
}
