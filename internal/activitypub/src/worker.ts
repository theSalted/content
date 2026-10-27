import { setTimeout } from 'node:timers/promises';
import type { Store, Intent } from './store.ts';

export type Enqueue = (intent: Intent) => Promise<void>;
export async function relayOne(store: Store, enqueue: Enqueue, now = new Date()): Promise<boolean> {
  const job = await store.claimIntent(now);
  if (!job) return false;
  try {
    // Crash after transport enqueue but before this transaction commits can re-enqueue the SAME activity ID.
    await store.handoffIntent(job,enqueue);
  } catch {
    await store.failIntent(job,now);
  }
  return true;
}
export async function relayLoop(store: Store, enqueue: Enqueue, signal: AbortSignal, onProgress:()=>void=()=>{}) {
  while (!signal.aborted) {
    await store.heartbeat();
    const handled=await relayOne(store,enqueue);
    onProgress();
    if (!handled) {
      try { await setTimeout(1000,undefined,{signal}); } catch (error) { if (!signal.aborted) throw error; }
    }
  }
}
