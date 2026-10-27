import { connect,forPublication } from '../src/db.ts';
import { DurableQueue } from '../src/queue.ts';
const schema=process.argv[2];
if(!schema||!/^fixture_[a-f0-9]{32}$/.test(schema)||!process.send)throw new Error('Only run from isolated PostgreSQL test');
const url=new URL(process.env.FEDERATION_TEST_DATABASE_URL!);
if(!['127.0.0.1','localhost','[::1]'].includes(url.hostname)||!url.pathname.endsWith('_test'))throw new Error('Local test database required');
const connection=connect(url.href,schema);
await new DurableQueue(forPublication(connection.db,'publication-example')).processOne(async()=>{
  process.send!('holding-job');await new Promise(()=>{});
});
