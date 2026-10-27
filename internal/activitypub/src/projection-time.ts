import {Temporal} from 'temporal-polyfill';

/** Revision order is authoritative. Keep the public projection's edit clock
 * strictly increasing even when source events share a timestamp or its clock
 * moves backward. Receivers such as Mastodon otherwise ignore valid edits.
 * Do not alter the original event timestamp or its idempotency hash.
 */
export function nextUpdatedAt(occurredAt:string,previous:unknown):string {
  if(typeof previous!=='string')return occurredAt;
  const minimum=Temporal.Instant.from(previous).add({milliseconds:1});
  return Temporal.Instant.compare(Temporal.Instant.from(occurredAt),minimum)<0?minimum.toString():occurredAt;
}
