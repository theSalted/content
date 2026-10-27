export function deliveryEnvironment(input: NodeJS.ProcessEnv, caPath?:string): Record<string,string>;
export function startDelivery(input:NodeJS.ProcessEnv,options?:{log?:(item:object)=>void}):Promise<{ready:()=>boolean;status:()=>{ready:boolean;running:boolean;paused:boolean};close:()=>Promise<void>}>;
