import {decodeDict,encodeDict,type Item,type InnerList} from 'structured-field-values';
import {ServiceError} from './contracts.ts';

type Dictionary=Record<string,Item|InnerList>;
const dictionary=(value:string):Dictionary=>Object.fromEntries(Object.entries(decodeDict(value)));
const REQUIRED=['@method','@target-uri','content-digest'];
const WINDOW_SECONDS=3600;
const MAX_SIGNATURES=3;

/** Application policy, not cryptographic verification. Fedify 2.4.5 verifies
 * RFC 9421 RSA signatures but does not require delivery coverage or enforce
 * expires. Only policy-eligible candidates may reach its verifier. Legacy
 * requests keep their existing Fedify verification path.
 *
 * Parameterized components are deliberately unsupported: the current Fedify
 * canonicalizer does not implement their full RFC semantics (e.g. ;sf, ;key).
 * A signature with such components must not be reinterpreted as raw headers.
 */
export function inboxSignatureHeaders(headers:Headers,now=Date.now()):Headers {
  if(!headers.has('signature-input'))return headers;
  const reject=()=>new ServiceError(401,'invalid_http_signature');
  const input=headers.get('signature-input')!,signature=headers.get('signature')??'';
  if(input.length>8192||signature.length>8192)throw reject();
  let inputs:Dictionary,signatures:Dictionary;
  try{inputs=dictionary(input);signatures=dictionary(signature);}catch{throw reject();}
  const eligible:Dictionary=Object.create(null),values:Dictionary=Object.create(null);
  // Match Fedify's bounded candidate count, including ineligible candidates.
  for(const [label,entry] of Object.entries(inputs).slice(0,MAX_SIGNATURES)){
    if(!Array.isArray(entry.value))continue;
    const names=entry.value.map((item:Item):unknown=>item.value);
    if(names.some(name=>typeof name!=='string')||new Set(names).size!==names.length)continue;
    if(entry.value.some((item:Item)=>Object.keys(item.params??{}).length>0)||!REQUIRED.every(name=>names.includes(name)))continue;
    const {created,expires,keyid}=entry.params??{};
    if(typeof keyid!=='string'||typeof created!=='number'||!Number.isSafeInteger(created)||Math.abs(now/1000-created)>WINDOW_SECONDS)continue;
    if(expires!==undefined&&(typeof expires!=='number'||!Number.isSafeInteger(expires)||expires<created||now/1000>expires))continue;
    const value=signatures[label];
    if(!value||!(value.value instanceof Uint8Array)||value.value.length===0)continue;
    eligible[label]=entry;values[label]=value;
  }
  if(Object.keys(eligible).length===0)throw reject();
  const filtered=new Headers(headers);
  filtered.set('signature-input',encodeDict(eligible));filtered.set('signature',encodeDict(values));
  return filtered;
}
