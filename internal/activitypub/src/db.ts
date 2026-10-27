import postgres from 'postgres';
import { schemaIdentifier,installationId } from './namespace.ts';
export type Row=Record<string,unknown>;
export interface Adapter {
 query<T extends Row=Row>(text:string,params?:unknown[]):Promise<T[]>;
 transaction<T>(work:(tx:Adapter)=>Promise<T>):Promise<T>;
}
export interface Database {
 readonly schema:string;readonly installation:string;readonly publication?:string;
 query<T extends Row=Row>(text:string,params?:unknown[]):Promise<T[]>;
 transaction<T>(work:(tx:Database)=>Promise<T>):Promise<T>;
}
// Expand only source-authored schema placeholders; values remain bound parameters.
export function database(adapter:Adapter,schema:string,installation:string):Database {
 const quoted=schemaIdentifier(schema);installationId(installation);
 return {schema,installation,
 query:<T extends Row>(text:string,params:unknown[]=[])=>adapter.query<T>(text.replaceAll('{{schema}}',quoted),params),
 transaction:<T>(work:(tx:Database)=>Promise<T>)=>adapter.transaction(tx=>work(database(tx,schema,installation)))};
}
// FORCE RLS scopes every read/write, including joins and queue subqueries.
// Context is transaction-local, never session-global. Untrusted clients cannot
// select a scope: the authenticated runtime configuration owns publication.
export function forPublication(db:Database,publication:string):Database {
 if(!publication||Buffer.byteLength(publication)>128||publication.includes('\0'))throw new Error('Invalid publication ID');
 const scoped=(tx:Database):Database=>({...tx,publication,transaction:work=>work(scoped(tx))});
 const transaction=async<T>(work:(tx:Database)=>Promise<T>)=>db.transaction(async tx=>{
 await tx.query("SELECT set_config('content.publication',$1,true)",[publication]);return work(scoped(tx));});
 return {schema:db.schema,installation:db.installation,publication,transaction,
 query:<T extends Row>(text:string,params:unknown[]=[])=>transaction(tx=>tx.query<T>(text,params))};
}
// Binding validation is also safe for a privileged, explicitly invoked importer.
// It intentionally does not impose runtime privilege restrictions.
export async function validateInstallationBinding(db:Database) {
 const [bound]=await db.query<{installation_id:string}>('SELECT installation_id FROM {{schema}}.installation WHERE singleton');
 if(bound?.installation_id!==db.installation)throw new Error('Schema belongs to another installation or is not migrated');
}
export async function validateRuntimeRole(db:Database) {
 // SET ROLE is authorized against session_user, while inherited privileges
 // apply immediately. Test every reachable effective role, including PUBLIC
 // and column-level grants through PostgreSQL's privilege inquiry functions.
 const [role]=await db.query<{unsafe:boolean}>(`WITH reachable AS (
 SELECT r.* FROM pg_roles r WHERE r.rolname=current_user OR r.rolname=session_user
 OR pg_has_role(session_user,r.oid,'SET')
 ), relations AS (
 SELECT c.oid,c.relowner,c.relname,n.oid AS namespace FROM pg_class c
 JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relkind IN ('r','p','v','m','S')
 ) SELECT EXISTS(SELECT 1 FROM reachable r WHERE r.rolsuper OR r.rolbypassrls OR r.rolcreaterole OR r.rolcreatedb OR r.rolreplication
 OR EXISTS(SELECT 1 FROM pg_roles target WHERE target.oid<>r.oid AND pg_has_role(r.oid,target.oid,'MEMBER WITH ADMIN OPTION'))
 OR EXISTS(SELECT 1 FROM pg_namespace n WHERE n.nspname=$1 AND has_schema_privilege(r.oid,n.oid,'CREATE'))
 OR EXISTS(SELECT 1 FROM relations c WHERE pg_has_role(r.oid,c.relowner,'USAGE'))
 OR EXISTS(SELECT 1 FROM relations c WHERE c.relname IN ('identities','installation','publication_roles','schema_migrations')
 AND (has_table_privilege(r.oid,c.oid,'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
 OR has_any_column_privilege(r.oid,c.oid,'INSERT,UPDATE,REFERENCES')))
 OR EXISTS(SELECT 1 FROM relations c WHERE c.relname='object_id_reservations'
 AND (has_table_privilege(r.oid,c.oid,'UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
 OR has_any_column_privilege(r.oid,c.oid,'UPDATE,REFERENCES')))
 ) AS unsafe`,[db.schema]);
 if(!role||role.unsafe)throw new Error('Runtime requires a non-owner NOBYPASSRLS role without privilege elevation or protected-table writes');
 await validateInstallationBinding(db);
}
export function connectionOptions(max:number,options:{sslCa?:string;profile?:'runtime'|'migration'}={}){
 if(!Number.isInteger(max)||max<2||max>20)throw new Error('Pool size must be 2..20');
 const migration=options.profile==='migration';
 return {max,connect_timeout:10,idle_timeout:20,onnotice:()=>{},
 ...(options.sslCa?{ssl:{ca:options.sslCa,rejectUnauthorized:true}}:{}),
 connection:{application_name:migration?'content-activitypub-migrator':'content-activitypub',
 statement_timeout:migration?60000:15000,lock_timeout:10000,idle_in_transaction_session_timeout:120000}};
}
export function connect(url:string,schema:string,installation='test-installation',max=5,options:{sslCa?:string;profile?:'runtime'|'migration'}={}){
 schemaIdentifier(schema);installationId(installation);
 const sql=postgres(url,connectionOptions(max,options));
 function wrap(c:typeof sql):Adapter{return {
 query:async<T extends Row>(text:string,params:unknown[]=[])=>Array.from(await c.unsafe(text,params as never[])) as T[],
 transaction:async<T>(work:(tx:Adapter)=>Promise<T>)=>await c.begin(async tx=>work(wrap(tx as unknown as typeof sql))) as T};}
 return {sql,db:database(wrap(sql),schema,installation),close:()=>sql.end({timeout:5})};
}
