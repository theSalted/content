import type {KvStore,KvKey,KvStoreSetOptions,KvStoreListEntry} from '@fedify/fedify';
import type {Database,Row} from './db.ts';
// Uses the same publication-scoped database and explicit schema as durable jobs.
export class DatabaseKvStore implements KvStore {
 readonly db:Database;
 constructor(db:Database){this.db=db;}
 async get<T>(key:KvKey):Promise<T|undefined>{
 const [row]=await this.db.query<Row & {value:T}>('SELECT value FROM {{schema}}.federation_kv WHERE key=$1 AND (expires_at IS NULL OR expires_at>clock_timestamp())',[key]);
 return row?.value;}
 async set(key:KvKey,value:unknown,options?:KvStoreSetOptions){
 const ttl=options?.ttl?.total('millisecond');
 if(ttl!==undefined&&(!Number.isFinite(ttl)||ttl<0))throw new Error('Invalid KV TTL');
 await this.db.query(`INSERT INTO {{schema}}.federation_kv(key,value,expires_at) VALUES($1,$2::text::jsonb,CASE WHEN $3::double precision IS NULL THEN NULL ELSE clock_timestamp()+$3*interval '1 millisecond' END)
 ON CONFLICT(publication_id,key) DO UPDATE SET value=EXCLUDED.value,expires_at=EXCLUDED.expires_at`,[key,JSON.stringify(value),ttl??null]);}
 async delete(key:KvKey){await this.db.query('DELETE FROM {{schema}}.federation_kv WHERE key=$1',[key]);}
 async *list<T>(prefix?:KvKey):AsyncIterable<KvStoreListEntry>{
 // Bounded caches still need an operator retention policy; cursor scan avoids
 // materializing all values and is adequate for Fedify's cache semantics.
 let after:string[]|null=null;
 for(;;){const rows:(Row & {key:string[];value:T})[]=await this.db.query<Row & {key:string[];value:T}>(`SELECT key,value FROM {{schema}}.federation_kv
 WHERE (expires_at IS NULL OR expires_at>clock_timestamp()) AND ($1::text[] IS NULL OR key[1:cardinality($1::text[])]=$1)
 AND ($2::text[] IS NULL OR key>$2) ORDER BY key LIMIT 100`,[prefix??null,after]);
 if(!rows.length)return;for(const row of rows)yield {key:row.key as [string,...string[]],value:row.value};after=rows.at(-1)!.key;}
 }
}
