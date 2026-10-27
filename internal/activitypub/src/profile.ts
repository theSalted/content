import {randomInt} from 'node:crypto';
import type {Config} from './config.ts';
/** The configured list wins over the single-image fallback. No cache-busting URLs. */
export function chooseBanner(config:Pick<Config,'imageUrl'|'imageUrls'>,index:(max:number)=>number=randomInt){
 const images=config.imageUrls;
 return images?.length?images[index(images.length)]:config.imageUrl;
}
/** Public collections are projections, never modifications to immutable event snapshots. */
export function withFeedbackCollections(payload:Record<string,unknown>,enabled?:boolean){
 const id=payload.id;
 return enabled&&typeof id==='string'?{...payload,replies:id+'/replies',likes:id+'/likes',shares:id+'/shares'}:payload;
}
