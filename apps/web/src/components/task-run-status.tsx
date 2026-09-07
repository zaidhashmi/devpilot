"use client";

import { useEffect, useState } from "react";
import { csrfToken } from "@/components/logout-button";
import type { TaskRun } from "@/lib/types";

const polling = new Set(["pending", "inspecting"]);
const cancellable = new Set(["pending", "inspecting", "awaiting_approval", "approved"]);

export function TaskRunStatus({ initial }: { initial: TaskRun }) {
  const [value, setValue] = useState(initial);
  const [comment, setComment] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    if (!polling.has(value.status)) return;
    const timer = setInterval(async () => {
      const response = await fetch(`/backend/api/v1/task-runs/${value.id}`, { cache: "no-store" });
      if (response.ok) setValue((await response.json()) as TaskRun);
    }, 1500);
    return () => clearInterval(timer);
  }, [value.id, value.status]);

  async function mutate(path: string, body?: object) {
    const response = await fetch(`/backend/api/v1/${path}`, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
      body: JSON.stringify(body ?? {}),
    });
    if (response.ok) {
      const fresh = await fetch(`/backend/api/v1/task-runs/${value.id}`, { cache: "no-store" });
      if (fresh.ok) setValue((await fresh.json()) as TaskRun);
      return;
    }
    const data = (await response.json().catch(() => null)) as { error?: { message?: string } } | null;
    setError(data?.error?.message ?? "The action could not be completed.");
  }

  const plan = value.plan_revision;
  const approval = value.approval;
  return <div className="mt-8 space-y-6">
    <section className="rounded-xl border p-6">
      <div className="flex justify-between"><div><p className="text-sm text-slate-500">Current stage</p><p className="mt-1 text-lg capitalize">{value.current_stage.replaceAll("_", " ")}</p></div><span className="capitalize text-slate-300">{value.status.replaceAll("_", " ")}</span></div>
      {value.status === "approved" && <p className="mt-4 text-sm text-emerald-300">Authorized for the next analysis phase. No downstream work is dispatched in Phase 4.</p>}
      {cancellable.has(value.status) && <button onClick={() => mutate(`task-runs/${value.id}/cancel`)} className="mt-5 rounded-md border border-red-800 px-3 py-2 text-sm text-red-300">Cancel run</button>}
      {error && <p className="mt-3 text-sm text-red-300">{error}</p>}
    </section>
    {value.workspace?.inspection_artifact && <section className="rounded-xl border p-6"><h2 className="text-xl font-semibold">Repository inspection</h2><p className="mt-3 text-sm text-slate-400">{value.workspace.inspection_artifact.regular_file_count} files · {value.workspace.inspection_artifact.total_bytes.toLocaleString()} bytes · {value.workspace.inspection_artifact.ecosystems.join(", ") || "No ecosystem detected"}</p></section>}
    {plan && <section className="rounded-xl border p-6"><h2 className="text-xl font-semibold">Deterministic workflow context</h2><p className="mt-2 text-sm text-amber-200">This is not an AI-generated implementation plan.</p><p className="mt-4 text-slate-300">{plan.summary}</p><dl className="mt-5 grid gap-4 sm:grid-cols-2"><div><dt className="text-xs text-slate-500">Repository</dt><dd>{plan.structured_payload.repository}</dd></div><div><dt className="text-xs text-slate-500">Next planned capability</dt><dd>{plan.structured_payload.recommended_next_stage.replaceAll("_", " ")}</dd></div></dl></section>}
    {approval && <section className="rounded-xl border p-6"><h2 className="text-xl font-semibold">{approval.status === "pending" ? "Human approval required" : "Approval decision"}</h2><p className="mt-2 text-sm text-slate-400">Approval authorizes the next read-only analysis phase, which is not implemented in Phase 4.</p>{approval.status === "pending" ? <div className="mt-5"><textarea value={comment} onChange={(event) => setComment(event.target.value)} maxLength={1000} placeholder="Optional decision comment" className="w-full rounded-md border bg-slate-950 p-3"/><div className="mt-3 flex gap-3"><button onClick={() => mutate(`approvals/${approval.id}/approve`, { comment })} className="rounded-md bg-emerald-500 px-4 py-2 text-slate-950">Approve</button><button onClick={() => mutate(`approvals/${approval.id}/reject`, { comment })} className="rounded-md border border-red-800 px-4 py-2 text-red-300">Reject</button></div></div> : <p className="mt-4 capitalize">{approval.status}{approval.decision_comment ? `: ${approval.decision_comment}` : ""}</p>}</section>}
  </div>;
}
