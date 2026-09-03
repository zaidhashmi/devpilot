"use client";

import { FormEvent, useState } from "react";
import { csrfToken } from "@/components/logout-button";

export function OrganizationForm({ initialName, canManage }: { initialName: string; canManage: boolean }) {
  const [name,setName]=useState(initialName);const [message,setMessage]=useState("");const [pending,setPending]=useState(false);
  async function submit(event:FormEvent){event.preventDefault();setPending(true);setMessage("");const response=await fetch("/backend/api/v1/organization",{method:"PATCH",headers:{"Content-Type":"application/json","X-CSRF-Token":csrfToken()},body:JSON.stringify({name})});const result=await response.json().catch(()=>null) as {error?:{message?:string}}|null;setMessage(response.ok?"Organization updated.":result?.error?.message??"Update failed.");setPending(false)}
  return <form className="mt-6 max-w-lg" onSubmit={submit}><label className="block text-sm text-slate-300">Organization name<input className="mt-2 w-full rounded-lg border bg-slate-950 px-3 py-2.5 disabled:opacity-60" disabled={!canManage} maxLength={120} minLength={1} onChange={(event)=>setName(event.target.value)} required value={name}/></label>{!canManage&&<p className="mt-2 text-sm text-slate-500">Only organization owners and administrators can update settings.</p>}<button className="mt-4 rounded-lg bg-emerald-400 px-4 py-2 font-medium text-slate-950 disabled:opacity-60" disabled={!canManage||pending} type="submit">{pending?"Saving…":"Save"}</button>{message&&<p className="mt-3 text-sm text-slate-300" role="status">{message}</p>}</form>
}
