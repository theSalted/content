import {z} from 'zod';
const settings=z.object({HOST:z.string().min(1).default('127.0.0.1'),
 PORT:z.coerce.number().int().min(1).max(65535).default(3000),
 INTERNAL_PORT:z.coerce.number().int().min(1).max(65535).optional(),
 SHUTDOWN_GRACE_MS:z.coerce.number().int().min(1000).max(60000).default(20000)});
export function processSettings(env:NodeJS.ProcessEnv){
 const p=settings.safeParse(env);if(!p.success)throw new Error('Invalid process settings');
 if(p.data.INTERNAL_PORT===p.data.PORT)throw new Error('Public and private listener ports must differ');
 return {host:p.data.HOST,port:p.data.PORT,internalPort:p.data.INTERNAL_PORT,shutdownGraceMs:p.data.SHUTDOWN_GRACE_MS};
}
export function shutdownDeadline(ms:number){
 const timer=setTimeout(()=>{console.error('Shutdown deadline reached; unfinished transactions will recover on restart.');process.exit(1);},ms);
 timer.unref();return ()=>clearTimeout(timer);
}
