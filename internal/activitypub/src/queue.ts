import { AsyncLocalStorage } from 'node:async_hooks';
import { setTimeout } from 'node:timers/promises';
import type { MessageQueue, MessageQueueEnqueueOptions, MessageQueueListenOptions } from '@fedify/fedify';
import type { Database,Row } from './db.ts';

type Job=Row & {sequence:string;message:unknown;attempts:number};
type Processing={tx:Database;job:Job;rescheduled:boolean};

// Acknowledge only AFTER the handler finishes. A process/connection crash rolls
// back the transaction, leaving the job available for another worker.
// A database transaction/connection stays open during handling: use a small
// number of dedicated workers, no transaction-mode pooler, and network timeouts.
export class DurableQueue implements MessageQueue {
  readonly nativeRetrial=true;
  readonly db:Database;
  readonly processing=new AsyncLocalStorage<Processing>();
  readonly onProgress:()=>void;
  constructor(db:Database,onProgress:()=>void=()=>{}){this.db=db;this.onProgress=onProgress;}

  async enqueue(message:unknown,options?:MessageQueueEnqueueOptions) {
    const delay=options?.delay?.total('millisecond')??0;
    if(!Number.isFinite(delay)||delay<0)throw new Error('Invalid queue delay');
    const active=this.processing.getStore();
    const id=(value:unknown)=>typeof value==='object'&&value!==null&&'id'in value?value.id:undefined;
    // Fedify may explicitly reschedule the same message for Retry-After or a
    // circuit hold. Keep its place, atomically, instead of letting Update/Delete
    // overtake Create. AsyncLocalStorage associates this with its own handler.
    if(active&&id(message)!=null&&id(message)===id(active.job.message)){
      await active.tx.query(`UPDATE {{schema}}.transport_jobs SET message=$2,available_at=clock_timestamp()+$3*interval '1 millisecond'
        WHERE sequence=$1`,[active.job.sequence,message,delay]);
      active.rescheduled=true;return;
    }
    await this.db.query(`INSERT INTO {{schema}}.transport_jobs(message,ordering_key,available_at)
      VALUES($1,$2,clock_timestamp()+$3*interval '1 millisecond')`,[message,options?.orderingKey??null,delay]);
  }
  async getDepth() {
    const row=(await this.db.query<{queued:string;ready:string}>(`SELECT count(*) AS queued,
      count(*) FILTER(WHERE available_at<=clock_timestamp()) AS ready FROM {{schema}}.transport_jobs WHERE NOT dead`))[0]!;
    return {queued:Number(row.queued),ready:Number(row.ready),delayed:Number(row.queued)-Number(row.ready)};
  }
  async processOne(handler:(message:unknown)=>void|Promise<void>):Promise<boolean> {
    return this.db.transaction(async tx=>{
      const job=(await tx.query<Job>(`SELECT q.sequence,q.message,q.attempts FROM {{schema}}.transport_jobs q
        WHERE NOT q.dead AND q.available_at<=clock_timestamp() AND NOT EXISTS
          (SELECT 1 FROM {{schema}}.transport_jobs p WHERE NOT p.dead AND p.ordering_key=q.ordering_key AND p.sequence<q.sequence)
        ORDER BY q.sequence FOR UPDATE OF q SKIP LOCKED LIMIT 1`))[0];
      if(!job)return false;
      const active:Processing={tx,job,rescheduled:false};
      try {
        await this.processing.run(active,()=>handler(job.message));
        if(!active.rescheduled)await tx.query('DELETE FROM {{schema}}.transport_jobs WHERE sequence=$1',[job.sequence]);
      }catch{
        const attempts=Number(job.attempts)+1;
        await tx.query(`UPDATE {{schema}}.transport_jobs SET attempts=$2,dead=$3,last_error='handler_failed',
          available_at=clock_timestamp()+$4*interval '1 millisecond' WHERE sequence=$1`,
        [job.sequence,attempts,attempts>=12,Math.min(3_600_000,1000*2**attempts)]);
      }
      return true;
    });
  }
  async listen(handler:(message:unknown)=>void|Promise<void>,options?:MessageQueueListenOptions) {
    while(!options?.signal?.aborted){
      const handled=await this.processOne(handler);
      this.onProgress();
      if(!handled){
        try{await setTimeout(1000,undefined,{signal:options?.signal});}
        catch(error){if(!options?.signal?.aborted)throw error;}
      }
    }
  }
}
