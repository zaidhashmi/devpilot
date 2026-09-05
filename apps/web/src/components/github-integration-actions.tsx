"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { csrfToken } from "@/components/logout-button";

export function GitHubIntegrationActions({connected,canManage,enabled}:{connected:boolean;canManage:boolean;enabled:boolean}){
  const router=useRouter();const [pending,setPending]=useState(false);const [message,setMessage]=useState("");
  async function mutate(method:"POST"|"DELETE",path:string){setPending(true);setMessage("");const response=await fetch(path,{method,headers:{"X-CSRF-Token":csrfToken()}});if(response.ok){router.refresh()}else{const body=await response.json().catch(()=>null) as {error?:{message?:string}}|null;setMessage(body?.error?.message??"The GitHub operation failed.")}setPending(false)}
  if(!canManage)return <p className="mt-6 text-sm text-slate-400">An organization owner or administrator manages this connection.</p>;
  if(!enabled)return <p className="mt-6 rounded-lg border border-amber-700/50 bg-amber-950/20 p-4 text-sm text-amber-200">GitHub integration is not configured for this deployment.</p>;
  return <div className="mt-6 flex flex-wrap items-center gap-3">{connected?<><button disabled={pending} onClick={()=>mutate("POST","/backend/api/v1/integrations/github/sync")} className="rounded-lg bg-emerald-400 px-4 py-2 font-medium text-slate-950 disabled:opacity-60">Refresh repositories</button><button disabled={pending} onClick={()=>mutate("DELETE","/backend/api/v1/integrations/github")} className="rounded-lg border border-red-800 px-4 py-2 text-red-300 disabled:opacity-60">Disconnect</button></>:<a className="rounded-lg bg-emerald-400 px-4 py-2 font-medium text-slate-950" href="/backend/api/v1/integrations/github/install">Connect GitHub</a>}{message&&<p role="status" className="w-full text-sm text-red-300">{message}</p>}</div>
}
