import {isIP} from 'node:net';
import {Parser} from 'htmlparser2';

/** Presentation-only aliases. These never authorize media access: the target
 * must independently enforce current publication visibility on every read. */
export type PublicMediaMapping={origin:string;sourceOrigins:readonly string[]};
const mediaPath=/^\/content\/images\/[a-f0-9]{24}\/image\.(?:png|jpg)$/;
const PUBLIC='https://www.w3.org/ns/activitystreams#Public';
function origin(value:string):string{
 let url:URL;try{url=new URL(value);}catch{throw new Error('Invalid public media origin');}
 if(url.protocol!=='https:'||url.username||url.password||url.search||url.hash||url.pathname!=='/'||
    url.origin!==value||isIP(url.hostname)||url.hostname.startsWith('[')||!url.hostname.includes('.')||
    /\.(?:localhost|local|internal)$/.test(url.hostname))throw new Error('Invalid public media origin');
 return url.origin;
}
export function readPublicMediaMapping(target?:string,sources?:string):PublicMediaMapping|undefined{
 if(target===undefined&&sources===undefined)return undefined;
 if(!target||!sources||target.length>2048||sources.length>16384)throw new Error('Public media origins must be configured together');
 const sourceOrigins=[...new Set(sources.split(',').map(origin))];
 if(sourceOrigins.length<1||sourceOrigins.length>8)throw new Error('Invalid public media source count');
 return {origin:origin(target),sourceOrigins};
}
export function publicMediaUrl(value:string,mapping:PublicMediaMapping):string{
 let url:URL;try{url=new URL(value);}catch{return value;}
 // Exact canonical tokens only; encoded paths, credentials, query strings,
 // fragments, relative URLs and arbitrary external hosts are not aliases.
 if(!mapping.sourceOrigins.includes(url.origin)||value!==url.origin+url.pathname||!mediaPath.test(url.pathname))return value;
 return mapping.origin+url.pathname;
}
export function publicMediaHtml(html:string,mapping:PublicMediaMapping):string{
 const edits:{start:number;end:number;value:string}[]=[];let tag='';
 const parser=new Parser({onopentagname(name){tag=name;},onattribute(name,_value){
  if(tag!=='img'||(name!=='src'&&name!=='srcset'))return;
  const start=parser.startIndex,end=parser.endIndex,raw=html.slice(start,end);
  const match=/^([^=]+=[\t\n\f\r ]*)(?:"([^"]*)"|'([^']*)'|([^\s>]+))$/.exec(raw);
  if(!match)return;
  const value=match[2]??match[3]??match[4]!;
  const changed=name==='src'?publicMediaUrl(value,mapping):value.split(',').map(candidate=>{
   const part=/^(\s*)(\S+)(\s+(?:\d+w|(?:\d+(?:\.\d+)?|\.\d+)x))?(\s*)$/.exec(candidate);
   return part?part[1]+publicMediaUrl(part[2]!,mapping)+(part[3]??'')+part[4]:candidate;
  }).join(',');
  if(value!==changed){const quote=match[2]!==undefined?'"':match[3]!==undefined?"'":'';edits.push({start,end,value:match[1]+quote+changed+quote});}
 }},{decodeEntities:false});
 parser.end(html);
 for(const edit of edits.reverse())html=html.slice(0,edit.start)+edit.value+html.slice(edit.end);
 return html;
}
function record(value:unknown):value is Record<string,unknown>{return !!value&&typeof value==='object'&&!Array.isArray(value);}
function isPublic(payload:Record<string,unknown>){return [payload.to,payload.cc].some(value=>value===PUBLIC||Array.isArray(value)&&value.includes(PUBLIC));}
/** Apply only after the caller's existing current-public access checks. */
export function projectArticleMedia(payload:Record<string,unknown>,mapping?:PublicMediaMapping):Record<string,unknown>{
 if(!mapping||payload.type!=='Article'||!isPublic(payload))return payload;
 const result=structuredClone(payload);
 if(typeof result.content==='string')result.content=publicMediaHtml(result.content,mapping);
 if(record(result.contentMap))for(const [language,value] of Object.entries(result.contentMap))if(typeof value==='string')result.contentMap[language]=publicMediaHtml(value,mapping);
 const attachment=(value:unknown)=>{
  if(!record(value)||value.type!=='Image')return value;
  if(typeof value.url==='string')value.url=publicMediaUrl(value.url,mapping);
  // AS Link-valued image URL is valid; its href is still subject to the exact alias/path rule.
  else if(record(value.url)&&value.url.type==='Link'&&typeof value.url.href==='string')value.url.href=publicMediaUrl(value.url.href,mapping);
  return value;
 };
 if(Array.isArray(result.attachment))result.attachment=result.attachment.map(attachment);
 else if(result.attachment!==undefined)result.attachment=attachment(result.attachment);
 return result;
}
/** Current outbox uses original activity identity and current public object. */
export function projectOutboxMedia(payload:Record<string,unknown>,mapping?:PublicMediaMapping):Record<string,unknown>{
 return mapping&&record(payload.object)?{...payload,object:projectArticleMedia(payload.object,mapping)}:payload;
}
