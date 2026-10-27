import {test} from 'node:test';
import assert from 'node:assert/strict';
import {inboxSignatureHeaders} from '../src/inbox-signature-policy.ts';

const now=1800000000000;
const full='("@method" "@target-uri" "content-digest")';
const params=';created=1800000000;keyid="https://peer.example/alice#key"';
const headers=(input:string,signature='sig=:AQID:')=>new Headers({'signature-input':input,signature});
test('legacy header path remains unmodified and empty modern header cannot downgrade',()=>{
 const legacy=new Headers({signature:'keyId="https://peer.example/key"'});
 assert.equal(inboxSignatureHeaders(legacy,now),legacy);
 assert.throws(()=>inboxSignatureHeaders(headers(''),now));
});
test('profile rejects omitted, duplicate and parameterized covered components',()=>{
 for(const input of ['("@method" "content-digest")','("@target-uri" "content-digest")','("@method" "@target-uri")',
  '("@method" "@target-uri" "content-digest" "content-digest")','("@method" "@target-uri" "content-digest";sf)']){
  assert.throws(()=>inboxSignatureHeaders(headers('sig='+input+params),now),input);
 }
});
test('creation and expiration bounds apply to each candidate',()=>{
 for(const p of [';created=1799992799',';created=1800007200',';created=1800000000;expires=1799999999',';created=1800000001;expires=1800000000',';expires=1800000001',';created=1800000000;expires="later"']){
  assert.throws(()=>inboxSignatureHeaders(headers('sig='+full+p+';keyid="https://peer.example/key"'),now));
 }
 assert.ok(inboxSignatureHeaders(headers('sig='+full+params+';expires=1800000060'),now));
});
test('filter binds coverage and expiry to the exact cryptographic candidate, preserving valid alternatives',()=>{
 const input='old='+full+params+';expires=1799999999, fresh='+full+params;
 const filtered=inboxSignatureHeaders(headers(input,'old=:AQID:, fresh=:BAUG:'),now);
 assert.equal(filtered.get('signature-input'),'fresh='+full+params);assert.equal(filtered.get('signature'),'fresh=:BAUG:');
 assert.throws(()=>inboxSignatureHeaders(headers(input,'old=:AQID:'),now));
});
test('modern profile bounds candidate count and header bytes before key lookup',()=>{
 const input=Array.from({length:4},(_,i)=>'s'+i+'='+full+params+(i<3?';expires=1799999999':'')).join(', ');
 assert.throws(()=>inboxSignatureHeaders(headers(input,'s0=:AQID:, s1=:AQID:, s2=:AQID:, s3=:AQID:'),now));
 assert.throws(()=>inboxSignatureHeaders(headers('sig='+full+params+';nonce="'+'a'.repeat(8192)+'"'),now));
});
