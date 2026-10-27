import { createHash } from 'node:crypto';
import type { Database } from './db.ts';
import type { Config } from './config.ts';

export type Identity = { actorId: string; keyId: string; privateKey: CryptoKey; publicKey: CryptoKey; fingerprint: string };
export async function fingerprint(key: CryptoKey): Promise<string> {
  return createHash('sha256').update(Buffer.from(await crypto.subtle.exportKey('spki', key))).digest('hex');
}
export async function loadIdentity(db: Database, config: Pick<Config, 'actorId'|'keyId'|'expectedFingerprint'>): Promise<Identity> {
  const rows = await db.query<{ actor_id: string; key_id: string; public_jwk: JsonWebKey; private_jwk: JsonWebKey }>(
    'SELECT actor_id,key_id,public_jwk,private_jwk FROM {{schema}}.identities WHERE actor_id = $1', [config.actorId]);
  const row = rows[0];
  if (!row) throw new Error('Existing identity is missing; startup refused. Restore/import verified identity before starting.');
  if (row.key_id !== config.keyId) throw new Error('Stored identity key identifier mismatch');
  if (row.public_jwk.d || row.public_jwk.k || row.public_jwk.kty !== 'RSA' || row.private_jwk.kty !== 'RSA')
    throw new Error('Invalid RSA identity bundle');
  const algorithm = { name: 'RSASSA-PKCS1-v1_5', hash: 'SHA-256' };
  const publicKey = await crypto.subtle.importKey('jwk', row.public_jwk, algorithm, true, ['verify']);
  // Fedify exports the key into its durable signed-delivery queue. Protect both tables and backups as secrets.
  const privateKey = await crypto.subtle.importKey('jwk', row.private_jwk, algorithm, true, ['sign']);
  const actual = await fingerprint(publicKey);
  if (actual !== config.expectedFingerprint) throw new Error('Public identity fingerprint mismatch');
  const challenge = new TextEncoder().encode('federation identity pair validation v1');
  const signature = await crypto.subtle.sign(algorithm, privateKey, challenge);
  if (!await crypto.subtle.verify(algorithm, publicKey, signature, challenge)) throw new Error('Identity public/private key mismatch');
  return { actorId: row.actor_id, keyId: row.key_id, publicKey, privateKey, fingerprint: actual };
}
