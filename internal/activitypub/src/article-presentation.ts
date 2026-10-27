import sanitizeHtml from 'sanitize-html';
import {sanitize} from './contracts.ts';
export type ImageAttachment={type:'Image';url:string;name:string;mediaType?:string};
// Read only the author's HTML; never fetch image URLs or extract hidden/private
// CMS fields. Active content, srcset and event handlers never enter AP payloads.
export function articlePresentation(source:string,permalink:string){
 const attachments:ImageAttachment[]=[];
 const imageByUrl=new Map<string,ImageAttachment>();
 sanitizeHtml(source,{allowedTags:['img'],allowedAttributes:{img:['src','alt']},allowedSchemes:['https'],allowProtocolRelative:false,
  transformTags:{img:(_tag,attributes)=>{
   let url:URL;try{url=new URL(attributes.src??'',permalink);}catch{return {tagName:'span',attribs:{} as Record<string,string>};}
   if(!attributes.src||url.protocol!=='https:'||url.username||url.password||url.hash)return {tagName:'span',attribs:{} as Record<string,string>};
   const alt=(attributes.alt??'').replaceAll('\0','').slice(0,1000);
   if(!imageByUrl.has(url.href)&&attachments.length<16){
    const extension=url.pathname.split('.').at(-1)?.toLowerCase();
    const mime:Record<string,string>={jpg:'image/jpeg',jpeg:'image/jpeg',png:'image/png',gif:'image/gif',webp:'image/webp',avif:'image/avif'};
    const attachment:ImageAttachment={type:'Image',url:url.href,name:alt,...(extension&&mime[extension]?{mediaType:mime[extension]}:{})};
    imageByUrl.set(url.href,attachment);attachments.push(attachment);
   }
   return {tagName:'img',attribs:{src:url.href,alt}};
  }}});
 // General HTML sanitizer still defines textual rendering; media are exposed
 // as explicit AS attachments for clients which strip inline image tags.
 return {html:sanitize(source),attachments};
}

/** Authored labels are preserved. Only hashtag-shaped labels become Hashtag;
 * spaces/punctuation remain normal AS Link labels, never guessed slug text. */
export function authoredTags(tags:readonly {name:string;href:string}[]=[]){
 return tags.map(tag=>/^[\p{L}\p{N}\p{M}_]+$/u.test(tag.name)
  ?{type:'Hashtag',name:'#'+tag.name,href:tag.href}
  :{type:'Link',name:tag.name,href:tag.href});
}
