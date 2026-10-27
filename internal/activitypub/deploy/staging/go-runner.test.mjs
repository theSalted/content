import {test} from 'node:test';
import assert from 'node:assert/strict';
import {goProbeEnvironment,redactedGoFailure} from './go-runner.fixture.mjs';
const input={PATH:'/fixture/bin',DATABASE_URL:'AP-runtime-must-not-leak',ACTIVITYPUB_MIGRATION_DATABASE_URL:'AP-owner-must-not-leak',CONTENT_MIGRATION_DATABASE_URL:'CMS-owner-must-not-leak',CONTENT_INSTALLATION_ID:'different-ap-binding',CONTENT_STAGING_CMS_API_DATABASE_URL:'synthetic-cms-api',CONTENT_STAGING_CMS_DELIVERY_DATABASE_URL:'synthetic-cms-delivery',CONTENT_STAGING_CMS_SCHEMA:'cms_fixture',CONTENT_STAGING_CMS_INSTALLATION_ID:'cms-installation',CONTENT_STAGING_DATABASE_SSL_CA:'synthetic-public-ca',PUBLICATION_ID:'synthetic-publication',PUBLISHER_ID:'content',PUBLISHER_TOKEN:'synthetic-scoped-publisher'};
const scope={privateBase:'http://127.0.0.1:3001',origin:'https://staging.example',runId:'synthetic-run'};
test('Go child receives explicit CMS binding and no AP/owner credentials',()=>{
 const env=goProbeEnvironment(input,scope);
 assert.equal(env.CONTENT_INSTALLATION_ID,'cms-installation');assert.equal(input.CONTENT_INSTALLATION_ID,'different-ap-binding');
 assert.equal(env.CONTENT_DATABASE_URL,'synthetic-cms-api');assert.equal(env.CONTENT_DELIVERY_DATABASE_URL,'synthetic-cms-delivery');
 assert.equal(env.CONTENT_PUBLISHING_ENDPOINT,'http://127.0.0.1:3001/internal/v2/events');
 assert.equal(env.CONTENT_PUBLISHING_TOKEN,input.PUBLISHER_TOKEN);assert.equal(env.CONTENT_STAGING_DATABASE_SSL_CA,'synthetic-public-ca');
 assert.equal(env.GOMEMLIMIT,'48MiB');assert.equal(env.GOMAXPROCS,'1');
 for(const key of ['DATABASE_URL','ACTIVITYPUB_MIGRATION_DATABASE_URL','CONTENT_MIGRATION_DATABASE_URL'])assert.equal(env[key],undefined);
 assert.ok(!JSON.stringify(env).includes('must-not-leak'));
});
test('missing CMS role or binding refuses instead of falling back to AP configuration',()=>{
 for(const key of ['CONTENT_STAGING_CMS_API_DATABASE_URL','CONTENT_STAGING_CMS_DELIVERY_DATABASE_URL','CONTENT_STAGING_CMS_SCHEMA','CONTENT_STAGING_CMS_INSTALLATION_ID']){
  const missing={...input};delete missing[key];assert.throws(()=>goProbeEnvironment(missing,scope));
 }
});

test('failed child diagnostics expose bounded codes only, never raw secret-shaped output',()=>{
 assert.equal(redactedGoFailure('{"status":"failed","failure":"peer_timeout"}'),'peer_timeout');
 for(const output of ['postgres://user:secret@host/db','{"status":"failed","failure":"postgres://user:secret@host/db"}','{"status":"failed","failure":"unbounded text with spaces"}'])assert.equal(redactedGoFailure(output),'nonzero_exit');
});
