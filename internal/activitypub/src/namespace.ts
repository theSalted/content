import { createHash } from 'node:crypto';
export function schemaIdentifier(schema:string):string {
 if(!/^[a-z_][a-z0-9_]{0,62}$/.test(schema)||schema==='public'||schema==='information_schema'||schema.startsWith('pg_'))throw new Error('A dedicated lowercase schema identifier is required');
 return '"'+schema+'"';
}
export function installationId(value:string):string {
 if(!value||Buffer.byteLength(value)>128||value.includes('\0'))throw new Error('Invalid installation ID');return value;
}
// Same signed big-endian SHA-256 derivation as Go; module separates locks.
export function lockId(...parts:string[]):string {return createHash('sha256').update(parts.join('\0')).digest().readBigInt64BE().toString();}
export const migrationLockId=(installation:string,schema:string)=>lockId('content',installation,'activitypub',schema);
export const ownershipLockId=(schema:string)=>lockId('content','activitypub','schema-ownership',schema);
