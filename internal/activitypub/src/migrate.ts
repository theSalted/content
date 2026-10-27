import {readFile} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import {pathToFileURL} from 'node:url';
import {connect,type Database} from './db.ts';
import {migrationLockId,ownershipLockId} from './namespace.ts';
export async function migrateSchema(db:Database){
 const migrations=await Promise.all(['001_initial.sql','002_object_id_reservations.sql','003_feedback.sql','004_follow_history.sql'].map(async file=>{
 const text=await readFile(new URL('../migrations/'+file,import.meta.url),'utf8');
 return {version:file.slice(0,3),text,checksum:createHash('sha256').update(text).digest('hex')};
 }));
 await db.transaction(async tx=>{
 await tx.query('SELECT pg_advisory_xact_lock($1::bigint)',[ownershipLockId(db.schema)]);
 await tx.query('SELECT pg_advisory_xact_lock($1::bigint)',[migrationLockId(db.installation,db.schema)]);
 if(!(await tx.query('SELECT 1 FROM pg_namespace WHERE nspname=$1',[db.schema])).length)throw new Error('Owned schema must already exist');
 const [namespace]=await tx.query<{occupied:boolean;ledger:string|null}>(`SELECT
 EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relkind IN ('r','p','S','v','m')) AS occupied,
 to_regclass($2)::text AS ledger`,[db.schema,'"'+db.schema+'".schema_migrations']);
 if(namespace?.occupied&&!namespace.ledger)throw new Error('Nonempty schema has no ActivityPub migration ledger; refusing another module namespace');
 await tx.query('CREATE TABLE IF NOT EXISTS {{schema}}.installation(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),installation_id text NOT NULL)');
 const [bound]=await tx.query<{installation_id:string}>('SELECT installation_id FROM {{schema}}.installation WHERE singleton');
 if(bound&&bound.installation_id!==db.installation)throw new Error('Schema belongs to another installation');
 await tx.query('CREATE TABLE IF NOT EXISTS {{schema}}.schema_migrations(version text PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())');
 for(const migration of migrations){
 const [applied]=await tx.query<{checksum:string}>('SELECT checksum FROM {{schema}}.schema_migrations WHERE version=$1',[migration.version]);
 if(applied){if(applied.checksum!==migration.checksum)throw new Error('Applied migration checksum mismatch');continue;}
 for(const statement of migration.text.split(';').filter(part=>part.trim()))await tx.query(statement);
 await tx.query('INSERT INTO {{schema}}.schema_migrations(version,checksum) VALUES($1,$2)',[migration.version,migration.checksum]);
 }
 if(!bound)await tx.query('INSERT INTO {{schema}}.installation(installation_id) VALUES($1)',[db.installation]);
 });
}
if(process.argv[1]&&pathToFileURL(process.argv[1]).href===import.meta.url){
 try{
 const {ACTIVITYPUB_MIGRATION_DATABASE_URL:dsn,ACTIVITYPUB_SCHEMA:schema,CONTENT_INSTALLATION_ID:installation}=process.env;
 if(!dsn||!schema||!installation)throw new Error('Explicit migration connection, schema and installation required');
 const c=connect(dsn,schema,installation,2,{profile:'migration',sslCa:process.env.ACTIVITYPUB_MIGRATION_SSL_CA});
 try{await migrateSchema(c.db);console.info('ActivityPub schema ready; no identity generated.');}finally{await c.close();}
 }catch{console.error('Migration refused: verify the dedicated connection, owned schema, installation and migration checksums.');process.exitCode=1;}
}
