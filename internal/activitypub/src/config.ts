import {z} from 'zod';
import {readPublicMediaMapping,type PublicMediaMapping} from './media-origins.ts';
import {sanitize} from './contracts.ts';
import {schemaIdentifier,installationId} from './namespace.ts';
const envSchema=z.object({
 DATABASE_URL:z.string().min(1),DATABASE_SSL_CA:z.string().min(1).optional(),ACTIVITYPUB_SCHEMA:z.string(),CONTENT_INSTALLATION_ID:z.string(),
 PUBLICATION_ID:z.string().min(1).max(128),ACTOR_ID:z.url(),ACTOR_KEY_ID:z.url(),
 PUBLIC_CONTENT_ORIGINS:z.string().optional(),PUBLIC_MEDIA_ORIGIN:z.string().optional(),PUBLIC_MEDIA_SOURCE_ORIGINS:z.string().optional(),
 ACTOR_NAME:z.string().min(1).max(200),ACTOR_PROFILE_URL:z.url(),
 ACTOR_SUMMARY:z.string().max(5000).optional(),ACTOR_ICON_URL:z.url().optional(),ACTOR_IMAGE_URL:z.url().optional(),
 ACTOR_IMAGE_URLS:z.string().max(65536).optional(),
 ACTOR_ICON_ALT:z.string().max(1000).optional(),ACTOR_IMAGE_ALT:z.string().max(1000).optional(),
 EXPECTED_PUBLIC_KEY_SHA256:z.string().regex(/^[a-f0-9]{64}$/),
 MODERATION_TOKEN:z.string().min(32).optional(),
 PUBLISHER_ID:z.string().regex(/^[a-z0-9][a-z0-9._-]{0,63}$/),PUBLISHER_TOKEN:z.string().min(32),
 FOLLOW_HISTORY_LIMIT:z.coerce.number().int().min(0).max(100).default(0),
 FOLLOW_HISTORY_INTERVAL_MS:z.coerce.number().int().min(1000).max(60000).default(2000),
 ACTIVITYPUB_FEEDBACK_ENABLED:z.enum(['true','false']).default('false'),
 ACTIVITYPUB_ARCHIVE_ENABLED:z.enum(['true','false']).default('false'),
 FEDERATION_ENABLED:z.enum(['true','false']).default('false'),HOST:z.string().default('127.0.0.1'),
 PORT:z.coerce.number().int().min(1).max(65535).default(3000),
 DATABASE_POOL_SIZE:z.coerce.number().int().min(5).max(20).default(5)
});
export type Config={databaseUrl:string;databaseSslCa?:string;schema:string;installationId:string;publicationId:string;
 actorId:string;keyId:string;origin:string;identifier:string;actorRoute:`${string}{identifier}`;displayName:string;profileUrl:string;
 publicMedia?:PublicMediaMapping;moderationToken?:string;followHistoryLimit?:number;followHistoryIntervalMs?:number;
 summary?:string;iconUrl?:string;imageUrl?:string;imageUrls?:string[];iconAlt?:string;imageAlt?:string;feedbackEnabled?:boolean;archiveEnabled?:boolean;expectedFingerprint:string;publisherId:string;publisherToken:string;federationEnabled:boolean;host:string;port:number;poolSize:number;contentOrigins?:string[]};
export function readConfig(env:NodeJS.ProcessEnv):Config{
 const parsed=envSchema.safeParse(env);
 if(!parsed.success)throw new Error('Invalid configuration: '+parsed.error.issues.map(i=>i.path.join('.')).join(', '));
 const e=parsed.data;if(e.MODERATION_TOKEN&&e.MODERATION_TOKEN===e.PUBLISHER_TOKEN)throw new Error('Moderation credential must differ from publisher credential');schemaIdentifier(e.ACTIVITYPUB_SCHEMA);installationId(e.CONTENT_INSTALLATION_ID);
 const actor=new URL(e.ACTOR_ID),profile=new URL(e.ACTOR_PROFILE_URL);
 const match=/^(\/[^?#{}]*\/)([a-z0-9_-]+)$/.exec(actor.pathname);
 if(actor.protocol!=='https:'||actor.username||actor.password||actor.search||actor.hash||!match)throw new Error('ACTOR_ID requires a canonical HTTPS path ending in an identifier');
 if(profile.protocol!=='https:'||profile.username||profile.password)throw new Error('Invalid profile URL');
 let imageUrls:string[]|undefined;
 if(e.ACTOR_IMAGE_URLS!==undefined){
  let parsed:unknown;try{parsed=JSON.parse(e.ACTOR_IMAGE_URLS);}catch{throw new Error('ACTOR_IMAGE_URLS requires a JSON URL array');}
  const result=z.array(z.url().max(2048)).min(1).max(32).safeParse(parsed);
  if(!result.success)throw new Error('ACTOR_IMAGE_URLS requires 1–32 URLs');
  imageUrls=[...new Set(result.data)];
 }
 for(const value of [e.ACTOR_ICON_URL,e.ACTOR_IMAGE_URL,...(imageUrls??[])])if(value){const u=new URL(value);if(u.protocol!=='https:'||u.username||u.password||u.hash)throw new Error('Invalid profile image URL');}
 // Explicit legacy-compatible RSA profile; never rewrite an imported key ID.
 if(e.ACTOR_KEY_ID!==e.ACTOR_ID+'#main-key')throw new Error('ACTOR_KEY_ID requires the preserved RSA #main-key profile');
 const contentOrigins=(e.PUBLIC_CONTENT_ORIGINS??actor.origin).split(',').map(value=>{const u=new URL(value);if(u.protocol!=='https:'||u.username||u.password||u.search||u.hash||u.pathname!=='/')throw new Error('Invalid public content origin');return u.origin;});
 return {publicMedia:readPublicMediaMapping(e.PUBLIC_MEDIA_ORIGIN,e.PUBLIC_MEDIA_SOURCE_ORIGINS),moderationToken:e.MODERATION_TOKEN,followHistoryLimit:e.FOLLOW_HISTORY_LIMIT,followHistoryIntervalMs:e.FOLLOW_HISTORY_INTERVAL_MS,imageUrls,feedbackEnabled:e.ACTIVITYPUB_FEEDBACK_ENABLED==='true',summary:e.ACTOR_SUMMARY===undefined?undefined:sanitize(e.ACTOR_SUMMARY),iconUrl:e.ACTOR_ICON_URL,imageUrl:e.ACTOR_IMAGE_URL,iconAlt:e.ACTOR_ICON_ALT,imageAlt:e.ACTOR_IMAGE_ALT,archiveEnabled:e.ACTIVITYPUB_ARCHIVE_ENABLED==='true',contentOrigins,databaseUrl:e.DATABASE_URL,databaseSslCa:e.DATABASE_SSL_CA,schema:e.ACTIVITYPUB_SCHEMA,installationId:e.CONTENT_INSTALLATION_ID,publicationId:e.PUBLICATION_ID,
 actorId:e.ACTOR_ID,keyId:e.ACTOR_KEY_ID,origin:actor.origin,identifier:match[2]!,actorRoute:(match[1]+'{identifier}') as `${string}{identifier}`,
 displayName:e.ACTOR_NAME,profileUrl:e.ACTOR_PROFILE_URL,expectedFingerprint:e.EXPECTED_PUBLIC_KEY_SHA256,
 publisherId:e.PUBLISHER_ID,publisherToken:e.PUBLISHER_TOKEN,federationEnabled:e.FEDERATION_ENABLED==='true',host:e.HOST,port:e.PORT,poolSize:e.DATABASE_POOL_SIZE};
}
