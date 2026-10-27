import {validateFeedbackStorage} from './feedback.ts';
import {DurableQueue} from './queue.ts';
import {DatabaseKvStore} from './kv.ts';
import {connect,forPublication,validateRuntimeRole} from './db.ts';
import {loadIdentity} from './identity.ts';
import {Store} from './store.ts';
import {makeFederation} from './federation.ts';
import type {Config} from './config.ts';
export async function runtime(config:Config,onTransportProgress:()=>void=()=>{}){
 const connection=connect(config.databaseUrl,config.schema,config.installationId,config.poolSize,{sslCa:config.databaseSslCa});
 try{
 await validateRuntimeRole(connection.db);
 if(!(await connection.db.query('SELECT 1 FROM {{schema}}.publication_roles WHERE publication_id=$1 AND role_name=current_user',[config.publicationId])).length)throw new Error('Runtime role is not bound to publication');
 const db=forPublication(connection.db,config.publicationId);
 if(config.feedbackEnabled)await validateFeedbackStorage(db);
 const identity=await loadIdentity(db,config);
 const state={config,identity,store:new Store(db,config.actorId,config.contentOrigins,{limit:config.followHistoryLimit??0,intervalMs:config.followHistoryIntervalMs??2000},config.publicMedia)};
 return {state,federation:makeFederation(state,new DatabaseKvStore(db),new DurableQueue(db,onTransportProgress)),close:connection.close};
 }catch(error){await connection.close();throw error;}
}
