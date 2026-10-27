import test from 'node:test';
import assert from 'node:assert/strict';
import { publishingEvent, publicDocument, sanitize } from '../src/contracts.ts';
import { readConfig } from '../src/config.ts';
import { published } from './helpers.ts';
import { readFile } from 'node:fs/promises';
import { z } from 'zod';

test('v2 public publishing contract accepts a valid event',()=>assert.equal(publishingEvent.safeParse(published()).success,true));
test('published JSON Schema matches the runtime contract and example is accepted',async()=>{
  const schema=JSON.parse(await readFile(new URL('../contracts/publishing-event.v2.schema.json',import.meta.url),'utf8'));
  assert.deepEqual(schema,{...z.toJSONSchema(publishingEvent),$id:'urn:content:activitypub:publishing-event:v2',title:'Content ActivityPub publishing event v2'});
  const example=JSON.parse(await readFile(new URL('../contracts/published.example.json',import.meta.url),'utf8'));
  assert.equal(publishingEvent.safeParse(example).success,true);
});
test('unknown versions and extra fields fail rather than silently changing semantics',()=>{
  for(const change of [{schema_version:99},{unknown:true},{revision:'0'},{revision:1.5},{source:{id:'ghost',epoch:'0'}}])
    assert.equal(publishingEvent.safeParse({...published(),...change}).success,false);
});
test('private draft and members content cannot be carried by publication events',()=>{
  for(const visibility of ['private','draft','members','paid']){
    const event=published();if(!('document'in event))throw new Error();
    assert.equal(publishingEvent.safeParse({...event,document:{...event.document,visibility}}).success,false);
  }
});
test('withdrawal/deletion events do not accept a document payload',()=>{
  assert.equal(publishingEvent.safeParse({...published(),type:'content.withdrawn'}).success,false);
});
test('HTML removes active content, embedded media and unsafe links',()=>{
  const result=sanitize('<script>secret()</script><img src=x onerror=alert(1)><p onclick=evil()>Hi <a href="javascript:alert(1)">bad</a><a href="https://example.org">good</a></p>');
  assert.doesNotMatch(result,/script|onerror|onclick|javascript:|<img/);
  assert.match(result,/https:\/\/example.org/);
});
test('public URLs reject credentials and non-HTTPS schemes',()=>{
  const event=published();if(!('document'in event))throw new Error();
  for(const url of ['http://example.org','javascript:alert(1)','https://user:pass@example.org'])
    assert.equal(publishingEvent.safeParse({...event,document:{...event.document,url}}).success,false);
});
test('runtime is disabled by default and needs an identity fingerprint',()=>{
  const values={ACTIVITYPUB_SCHEMA:'example_ap',CONTENT_INSTALLATION_ID:'example',PUBLICATION_ID:'example',ACTOR_ID:'https://example.org/actors/editor',ACTOR_KEY_ID:'https://example.org/actors/editor#main-key',ACTOR_NAME:'Editor',ACTOR_PROFILE_URL:'https://example.org/about',DATABASE_URL:'postgres://test',EXPECTED_PUBLIC_KEY_SHA256:'a'.repeat(64),PUBLISHER_ID:'cms',PUBLISHER_TOKEN:'x'.repeat(40)};
  assert.equal(readConfig(values).federationEnabled,false);
  assert.throws(()=>readConfig({...values,DATABASE_POOL_SIZE:'2'}),/DATABASE_POOL_SIZE/);
  assert.throws(()=>readConfig({...values,EXPECTED_PUBLIC_KEY_SHA256:undefined}),/EXPECTED_PUBLIC_KEY_SHA256/);
  assert.throws(()=>readConfig({...values,ACTOR_KEY_ID:'https://example.org/actors/editor#replacement'}),/preserved/);
});

test('HTML limit counts UTF8 bytes without silently truncating source',()=>{
 const input=published();
 for(const html of ['a'.repeat(1_048_576),'中'.repeat(349525)])assert.equal(publishingEvent.safeParse({...input,document:{...input.document,html}}).success,true);
 for(const html of ['a'.repeat(1_048_577),'中'.repeat(349526),'hello\0world'])assert.equal(publishingEvent.safeParse({...input,document:{...input.document,html}}).success,false);
});

test('shared Unicode title boundaries count code points and preserve the exact text',async()=>{
 const cases=JSON.parse(await readFile(new URL('../contracts/fixtures/v2-cases.json',import.meta.url),'utf8'));
 const schema=z.toJSONSchema(publicDocument.shape.title);
 assert.equal(schema.minLength,1);assert.equal(schema.maxLength,255);
 const pattern=new RegExp(schema.pattern!,'u');
 for(const c of cases.filter((c:{name:string})=>c.name.startsWith('title-'))){
  const title=c.event.document.title;const parsed=publishingEvent.safeParse(c.event);
  assert.equal(parsed.success,c.valid,c.name);
  const codepoints=Array.from(title).length;
  assert.equal(codepoints>=1&&codepoints<=255&&pattern.test(title),c.valid,c.name+' JSON Schema');
  if(parsed.success&&'document' in parsed.data)assert.equal(parsed.data.document.title,title,c.name+' no normalization/truncation');
 }
 for(const title of ['\uD800','\uDFFF'])assert.equal(publicDocument.shape.title.safeParse(title).success,false);
});
