import { pathToFileURL } from 'node:url';
import { z } from 'zod';
import type { Database } from './db.ts';
import { connect,forPublication,validateInstallationBinding } from './db.ts';
import { readConfig,type Config } from './config.ts';
import { loadIdentity } from './identity.ts';

const bundleSchema=z.strictObject({actor_id:z.string(),key_id:z.string(),
  public_jwk:z.record(z.string(),z.unknown()),private_jwk:z.record(z.string(),z.unknown())});

export async function importExistingIdentity(db:Database,config:Config,value:unknown) {
  if(config.schema!==db.schema||config.installationId!==db.installation||config.publicationId!==db.publication)throw new Error('Identity import configuration does not match database binding');
  const bundle=bundleSchema.parse(value);
  if(bundle.actor_id!==config.actorId||bundle.key_id!==config.keyId)throw new Error('Identity identifiers mismatch');
  return db.transaction(async tx=>{
    await validateInstallationBinding(tx);
    // INSERT only: existing identity is never overwritten, even by an identical bundle.
    await tx.query('INSERT INTO {{schema}}.identities(actor_id,key_id,public_jwk,private_jwk) VALUES($1,$2,$3,$4)',
      [bundle.actor_id,bundle.key_id,bundle.public_jwk,bundle.private_jwk]);
    // Wrong fingerprint or key pair rolls the entire import back.
    const identity=await loadIdentity(tx,config);
    return {actor_id:identity.actorId,key_id:identity.keyId,fingerprint:identity.fingerprint};
  });
}
if(process.argv[1]&&pathToFileURL(process.argv[1]).href===import.meta.url){
  try{
    const config=readConfig(process.env);
    const chunks:Buffer[]=[];let size=0;
    for await(const chunk of process.stdin){size+=chunk.length;if(size>32_768)throw new Error('Identity bundle too large');chunks.push(Buffer.from(chunk));}
    const connection=connect(config.databaseUrl,config.schema,config.installationId,config.poolSize);
    try{console.info(JSON.stringify(await importExistingIdentity(forPublication(connection.db,config.publicationId),config,JSON.parse(Buffer.concat(chunks).toString('utf8')))));}
    finally{await connection.close();}
  }catch{console.error('Identity import refused; no existing identity was replaced. Verify identifiers, fingerprint and bundle offline.');process.exitCode=1;}
}
