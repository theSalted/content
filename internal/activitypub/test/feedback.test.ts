import {test,before,after,beforeEach} from 'node:test';import assert from 'node:assert/strict';import {randomUUID} from 'node:crypto';
import {fixture,published} from './helpers.ts';import {FeedbackStore} from '../src/feedback.ts';
let f:Awaited<ReturnType<typeof fixture>>,feedback:FeedbackStore,target:string;
const actor='https://remote.example/users/commenter';
before(async()=>{f=await fixture();feedback=new FeedbackStore(f.db);});after(async()=>{await f.close();});
beforeEach(async()=>{await f.reset();target=(await f.store.publish(published(),'ghost')).object_id!;});
const reply=()=>{const id='https://remote.example/notes/'+randomUUID();return {id,activityId:id+'/create',actor,target,kind:'reply' as const,html:'<p>Hello <script>bad()</script><a href="javascript:bad()">reader</a></p>',updatedAt:'2026-10-26T00:00:00Z'};};
test('public local replies are sanitized and private until owner approves',async()=>{
 const r=reply();assert.equal(await feedback.receive(r),'pending');assert.equal((await feedback.visible(target)).length,0);
 const pending=await feedback.list();assert.equal(pending.length,1);assert.doesNotMatch(pending[0]!.html,/script|javascript:/);
 await moderateCurrent(feedback,r.id,'approved');assert.equal((await feedback.visible(target))[0]!.actor_id,actor);
 assert.equal((await f.db.query('SELECT * FROM {{schema}}.outbound_intents')).length,0);
 await feedback.receive({...r,activityId:r.id+'/update',updatedAt:'2026-10-26T00:01:00Z',html:'<p>Changed</p>'});assert.equal((await feedback.visible(target)).length,0);assert.equal((await feedback.list())[0]!.html,'<p>Changed</p>');
});
test('duplicate/stale updates do not replace newer text or restore deleted feedback',async()=>{
 const r=reply();await feedback.receive(r);assert.equal(await feedback.receive(r),'stale');
 await feedback.receive({...r,activityId:r.id+'/update',updatedAt:'2026-10-26T00:02:00Z',html:'<p>New</p>'});
 assert.equal(await feedback.receive({...r,activityId:r.id+'/old'}),'stale');
 await feedback.remove(actor,r.id);assert.equal(await feedback.receive({...r,activityId:r.id+'/later',updatedAt:'2026-10-26T01:00:00Z'}),'deleted');
 assert.equal((await feedback.list('deleted'))[0]!.html,'');
 await assert.rejects(moderateCurrent(feedback,r.id,'approved'),/feedback_not_found/);
});
test('forged ownership, nonpublic local targets and cross-publication targets cannot enter moderation',async()=>{
 const r=reply();await assert.rejects(feedback.receive({...r,actor:'https://attacker.example/a'}),/feedback_owner_mismatch/);
 assert.equal(await feedback.receive({...r,target:'https://another.example/ap/objects/x/y'}),'not_public_target');
 await feedback.receive(r);await assert.rejects(feedback.remove('https://attacker.example/a',r.id),/feedback_owner_mismatch/);
 await f.db.query("UPDATE {{schema}}.content_objects SET state='withdrawn',payload=NULL WHERE object_id=$1",[target]);
 assert.equal(await feedback.receive(reply()),'not_public_target');await moderateCurrent(feedback,r.id,'approved');assert.equal((await feedback.visible(target)).length,0);
});
test('bounded pending queue and actor block prevent repeated unmoderated spam',async()=>{
 for(let i=0;i<20;i++)assert.equal(await feedback.receive(reply()),'pending');assert.equal(await feedback.receive(reply()),'limited');
 await feedback.block(actor,true);assert.equal((await feedback.list()).length,0);assert.equal(await feedback.receive(reply()),'blocked');
 await feedback.block(actor,false);assert.equal(await feedback.receive(reply()),'pending');
});
test('reactions and boosts retain attribution, and removal before arrival suppresses replay',async()=>{
 for(const kind of ['like','boost'] as const){const r={...reply(),kind,html:''};await feedback.receive(r);await moderateCurrent(feedback,r.id,'approved');await feedback.remove(actor,r.id);}
 const late=reply();await feedback.remove(actor,late.id);assert.equal(await feedback.receive(late),'deleted');assert.equal((await feedback.visible(target)).length,0);
});

test('same-origin accounts cannot poison another author with an unknown-object removal',async()=>{
 const r=reply();await feedback.remove('https://remote.example/users/attacker',r.id);
 assert.equal(await feedback.receive(r),'pending');await moderateCurrent(feedback,r.id,'approved');
 await assert.rejects(feedback.remove('https://remote.example/users/attacker',r.id),/feedback_owner_mismatch/);
 await feedback.block(actor,true);await assert.rejects(moderateCurrent(feedback,r.id,'approved'),/feedback_not_found/);assert.equal((await feedback.visible(target)).length,0);
});

async function moderateCurrent(store:FeedbackStore,id:string,decision:'approved'|'rejected'){
 const [row]=await store.db.query<{activity_id:string}>('SELECT activity_id FROM {{schema}}.feedback WHERE id=$1',[id]);
 return store.moderate(id,decision,row?.activity_id??'https://invalid.example/missing');
}
