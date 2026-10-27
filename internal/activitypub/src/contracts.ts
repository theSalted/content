import { z } from 'zod';
import { createHash } from 'node:crypto';
import sanitizeHtml from 'sanitize-html';

const httpsUrl = z.url().refine(value => {
  const url = new URL(value);
  return url.protocol === 'https:' && !url.username && !url.password;
}, 'An HTTPS URL without credentials is required');
const timestamp = z.iso.datetime({ offset: true });
const counter = z.string().regex(/^[1-9][0-9]{0,18}$/).refine(v=>BigInt(v)<=9223372036854775807n,'signed 64-bit counter required');
const base = {
  schema_version: z.literal(2),
  publication_id: z.string().min(1).max(128).refine(v=>Buffer.byteLength(v)<=128&&!v.includes('\0'),'publication ID must be 1..128 UTF8 bytes without NUL'),
  event_id: z.uuid(),
  content_id: z.uuid(),
  object_instance: z.uuid(),
  delivery_policy: z.literal('live'),
  source: z.strictObject({ id: z.string().regex(/^[a-z0-9][a-z0-9._-]{0,63}$/), epoch: counter }),
  revision: counter,
  occurred_at: timestamp
};
// Match Go's UTF-8 rune count and strings.TrimSpace Unicode White_Space
// semantics. Count combining marks separately; never normalize or truncate.
const unicodeWhitespace = /^[\p{White_Space}]*$/u;
const publicTitle = z.string().refine(value => {
  const count = Array.from(value).length;
  return count >= 1 && count <= 255 && !unicodeWhitespace.test(value)
    && !/[\u0000\uD800-\uDFFF]/u.test(value);
}, 'Title requires 1..255 Unicode code points, nonblank, no NUL, and valid Unicode').meta({
  minLength: 1,
  maxLength: 255,
  pattern: '^(?![\\u0009-\\u000D\\u0020\\u0085\\u00A0\\u1680\\u2000-\\u200A\\u2028\\u2029\\u202F\\u205F\\u3000]*$)[^\\u0000]*$',
  description: '1..255 Unicode code points; not solely Unicode White_Space; no NUL. Combining marks count separately. Preserved without normalization or truncation.'
});
const tagName=z.string().refine(value=>{
 const length=Array.from(value).length;
 return length>=1&&length<=100&&!unicodeWhitespace.test(value)&&!/[\u0000\uD800-\uDFFF]/u.test(value)&&!(/^[\p{White_Space}]*#/u.test(value));
},'Tag name requires 1..100 valid Unicode code points, nonblank, no NUL and no internal # prefix').meta({minLength:1,maxLength:100,description:'Exact authored public label, not an internal # tag; no normalization or truncation.'});
export const publicTag=z.strictObject({name:tagName,href:httpsUrl.max(2048).refine(value=>{const u=new URL(value);return !u.search&&!u.hash;},'Canonical tag URL cannot contain query or fragment')});
const publicTags=z.array(publicTag).max(32).refine(tags=>new Set(tags.map(tag=>tag.href)).size===tags.length,'Duplicate tag URLs are not allowed');
export const publicDocument = z.strictObject({
  visibility: z.literal('public'),
  title: publicTitle,
  html: z.string().max(1_048_576).refine(v=>Buffer.byteLength(v)<=1_048_576&&!v.includes('\0'),'HTML exceeds UTF8 byte limit or contains NUL'),
  url: httpsUrl,
  published_at: timestamp,
  language: z.string().regex(/^[a-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$/).optional(),
  tags:publicTags.optional()
});
export const publishingEvent = z.discriminatedUnion('type', [
  z.strictObject({ ...base, type: z.literal('content.published'), document: publicDocument }),
  z.strictObject({ ...base, type: z.literal('content.updated'), document: publicDocument }),
  z.strictObject({ ...base, type: z.literal('content.withdrawn') }),
  z.strictObject({ ...base, type: z.literal('content.deleted') })
]);
// Explicit historical enrollment, never accepted by the live event endpoint.
export const archiveSnapshot = z.strictObject({
  ...base, schema_version:z.literal(1), type:z.literal('content.archived'),
  delivery_policy:z.literal('archive'), source_state:z.literal('published'), document:publicDocument
});
export type ArchiveSnapshot = z.infer<typeof archiveSnapshot>;
export type PublishingEvent = z.infer<typeof publishingEvent>;
export type PublicDocument = z.infer<typeof publicDocument>;
export function eventHash(event: PublishingEvent | ArchiveSnapshot): string {
  return createHash('sha256').update(canonicalJson(event)).digest('hex');
}
export function sanitize(content: string): string {
  return sanitizeHtml(content, {
    allowedTags: ['p','br','a','strong','em','b','i','u','del','pre','code','blockquote','ul','ol','li','h1','h2','h3','h4'],
    allowedAttributes: { a: ['href','title'], ol: ['start'], li: ['value'] },
    allowedSchemes: ['https','http','mailto'],
    allowProtocolRelative: false,
    transformTags: { a: sanitizeHtml.simpleTransform('a', { rel: 'nofollow noopener noreferrer' }) }
  });
}
export class ServiceError extends Error {
  readonly status: number;
  readonly code: string;
  constructor(status: number, code: string) { super(code); this.status = status; this.code = code; }
}
export function remoteUrl(value: string): string {
  if (!httpsUrl.safeParse(value).success) throw new ServiceError(422, 'invalid_remote_url');
  return value;
}

// Canonical field order, independent of Go JSON field order/HTML escaping.
export function canonicalJson(value:unknown):string {
 if(value===null||typeof value!=='object')return JSON.stringify(value);
 if(Array.isArray(value))return '['+value.map(canonicalJson).join(',')+']';
 return '{'+Object.entries(value).sort(([a],[b])=>a<b?-1:a>b?1:0).map(([k,v])=>JSON.stringify(k)+':'+canonicalJson(v)).join(',')+'}';
}
