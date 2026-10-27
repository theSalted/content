import { writeFile } from 'node:fs/promises';
import { z } from 'zod';
import { publishingEvent,archiveSnapshot } from '../src/contracts.ts';
const schema={...z.toJSONSchema(publishingEvent),$id:'urn:content:activitypub:publishing-event:v2',title:'Content ActivityPub publishing event v2'};
await writeFile(new URL('../contracts/publishing-event.v2.schema.json',import.meta.url),JSON.stringify(schema,null,2)+'\n');

await writeFile(new URL('../contracts/archive-v1/schema.json',import.meta.url),JSON.stringify({...z.toJSONSchema(archiveSnapshot),$id:'urn:content:activitypub:archive:v1',title:'Content ActivityPub silent archive v1'},null,2)+'\n');
