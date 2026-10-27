// Independent, deliberately bounded RFC 9421 / RFC 9530 RSA test peer.
// No Fedify signing, verification, canonicalization or structured-field code.
import {createHash,createPublicKey,KeyObject,sign,verify} from 'node:crypto';
export const components=['@method','@target-uri','content-digest'];
export const contentDigest=(body:Uint8Array)=>'sha-256=:'+createHash('sha256').update(body).digest('base64')+':';
const quoted=(s:string)=>'"'+s.replaceAll('\\','\\\\').replaceAll('"','\\"')+'"';
export type RfcOptions={components?:string[];created?:number|null;expires?:number;algorithm?:string|null;label?:string;digest?:string;signUrl?:string;signMethod?:string};
function base(url:string,method:string,headers:Headers,names:string[],params:string){
 const u=new URL(url);const derived:Record<string,string>={'@method':method.toUpperCase(),'@target-uri':u.href,'@authority':u.host};
 return [...names.map(name=>{
  const value=name.startsWith('@')?derived[name]:headers.get(name);
  if(value==null)throw Error('Unsupported or missing test component: '+name);
  return quoted(name)+': '+value;
 }), '"@signature-params": '+params].join('\n');
}
export function signRfc(url:string,value:unknown,key:CryptoKey,keyId:string,options:RfcOptions={}):Request {
 const body=Buffer.from(JSON.stringify(value));const names=options.components??components;
 const headers=new Headers({'content-type':'application/activity+json','content-digest':options.digest??contentDigest(body)});
 const created=options.created===undefined?Math.floor(Date.now()/1000):options.created;
 const alg=options.algorithm===undefined?'rsa-v1_5-sha256':options.algorithm;
 const params='('+names.map(quoted).join(' ')+')'+(created===null?'':';created='+created)+';keyid='+quoted(keyId)+(alg===null?'':';alg='+quoted(alg))+(options.expires===undefined?'':';expires='+options.expires);
 const signature=sign('RSA-SHA256',Buffer.from(base(options.signUrl??url,options.signMethod??'POST',headers,names,params)),KeyObject.from(key));
 const label=options.label??'peer';
 headers.set('signature-input',label+'='+params);headers.set('signature',label+'=:'+signature.toString('base64')+':');
 return new Request(url,{method:'POST',headers,body});
}
export function verifyRfc(url:string,method:string,headers:Headers,body:Uint8Array,publicKey:{id:string;owner:string;publicKeyPem:string},actor:string,now=Date.now()):boolean {
 // Strict single-signature profile. Reject unsupported syntax rather than
 // accidentally claiming to implement every RFC structured-field variation.
 const input=/^([a-z][a-z0-9_-]*)=(\((?:"[@a-z0-9-]+"(?: )?)+\))((?:;[a-z]+=(?:"[^"\\]*"|[0-9]+))+)$/u.exec(headers.get('signature-input')??'');
 if(!input)return false;
 const names=[...input[2]!.matchAll(/"([^"]+)"/g)].map(m=>m[1]!);
 const entries=[...input[3]!.matchAll(/;([a-z]+)=(?:"([^"\\]*)"|([0-9]+))/g)].map(m=>[m[1]!,m[2]??m[3]!] as const);
 const params=Object.fromEntries(entries);const created=Number(params.created),expires=params.expires===undefined?undefined:Number(params.expires);
 if(new Set(entries.map(e=>e[0])).size!==entries.length||new Set(names).size!==names.length||!components.every(c=>names.includes(c)))return false;
 if(!Number.isSafeInteger(created)||Math.abs(now/1000-created)>3600||(expires!==undefined&&(!Number.isSafeInteger(expires)||expires<now/1000||expires<created)))return false;
 if(params.keyid!==publicKey.id||publicKey.owner!==actor||params.alg!=='rsa-v1_5-sha256'||headers.get('content-digest')!==contentDigest(body))return false;
 const sig=new RegExp('^'+input[1]+'=:([A-Za-z0-9+/]+={0,2}):$').exec(headers.get('signature')??'');if(!sig)return false;
 try{return verify('RSA-SHA256',Buffer.from(base(url,method,headers,names,input[2]!+input[3]!)),createPublicKey(publicKey.publicKeyPem),Buffer.from(sig[1]!,'base64'));}catch{return false;}
}
