"use client";

import { useEffect, useState } from "react";
import { csrfToken } from "@/components/logout-button";
import type { TaskRun } from "@/lib/types";

const polling = new Set(["pending", "inspecting", "analyzing"]);
const cancellable = new Set(["pending", "inspecting", "analyzing", "awaiting_approval", "approved"]);

export function TaskRunStatus({ initial }: { initial: TaskRun }) {
  const [value, setValue] = useState(initial);
  const [comment, setComment] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    if (!polling.has(value.status) && !(value.status === "approved" && value.current_stage === "approval")) return;
    const timer = setInterval(async () => {
      const response = await fetch(`/backend/api/v1/task-runs/${value.id}`, { cache: "no-store" });
      if (response.ok) setValue((await response.json()) as TaskRun);
    }, 1500);
    return () => clearInterval(timer);
  }, [value.id, value.status, value.current_stage]);

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
  const analysis = value.analysis_revision;
  return <div className="mt-8 space-y-6">
    <section className="rounded-xl border p-6">
      <div className="flex justify-between"><div><p className="text-sm text-slate-500">Current stage</p><p className="mt-1 text-lg capitalize">{value.current_stage.replaceAll("_", " ")}</p></div><span className="capitalize text-slate-300">{value.status.replaceAll("_", " ")}</span></div>
      {value.status === "approved" && <p className="mt-4 text-sm text-emerald-300">{value.current_stage === "analysis_approval" ? "Repository analysis was reviewed and the future planning phase is authorized. Planning is not implemented yet." : "Repository analysis is authorized and durably queued."}</p>}
      {cancellable.has(value.status) && <button onClick={() => mutate(`task-runs/${value.id}/cancel`)} className="mt-5 rounded-md border border-red-800 px-3 py-2 text-sm text-red-300">Cancel run</button>}
      {error && <p className="mt-3 text-sm text-red-300">{error}</p>}
    </section>
    {value.workspace?.inspection_artifact && <section className="rounded-xl border p-6"><h2 className="text-xl font-semibold">Repository inspection</h2><p className="mt-3 text-sm text-slate-400">{value.workspace.inspection_artifact.regular_file_count} files · {value.workspace.inspection_artifact.total_bytes.toLocaleString()} bytes · {value.workspace.inspection_artifact.ecosystems.join(", ") || "No ecosystem detected"}</p></section>}
    {plan && <section className="rounded-xl border p-6"><h2 className="text-xl font-semibold">Deterministic workflow context</h2><p className="mt-2 text-sm text-amber-200">This is not an AI-generated implementation plan.</p><p className="mt-4 text-slate-300">{plan.summary}</p><dl className="mt-5 grid gap-4 sm:grid-cols-2"><div><dt className="text-xs text-slate-500">Repository</dt><dd>{plan.structured_payload.repository}</dd></div><div><dt className="text-xs text-slate-500">Next planned capability</dt><dd>{plan.structured_payload.recommended_next_stage.replaceAll("_", " ")}</dd></div></dl></section>}
    {value.agent_run && <section className="rounded-xl border p-6"><h2 className="text-xl font-semibold">Repository Analysis Agent</h2><dl className="mt-4 grid gap-4 sm:grid-cols-4"><div><dt className="text-xs text-slate-500">Status</dt><dd className="capitalize">{value.agent_run.status}</dd></div><div><dt className="text-xs text-slate-500">Provider</dt><dd>{value.agent_run.provider === "ollama" ? "Ollama (Local)" : "OpenAI"}</dd></div><div><dt className="text-xs text-slate-500">Model</dt><dd>{value.agent_run.model}</dd></div><div><dt className="text-xs text-slate-500">Prompt</dt><dd>{value.agent_run.prompt_version}</dd></div></dl></section>}
    {analysis && <section className="rounded-xl border p-6"><h2 className="text-xl font-semibold">AI-generated repository analysis</h2><p className="mt-2 text-sm text-amber-200">Evidence should be reviewed before acting on findings.</p>{analysis.structured_payload.coverage && <p className="mt-3 text-xs text-slate-500">Coverage: {analysis.structured_payload.coverage.files_read} files read from {analysis.structured_payload.coverage.files_offered} offered ({analysis.structured_payload.coverage.actual_file_count} cataloged){analysis.structured_payload.coverage.repository_index_truncated ? " · index truncated" : ""}{analysis.structured_payload.coverage.evidence_bundle_truncated ? " · evidence bundle truncated" : ""} · {analysis.structured_payload.coverage.source_bytes.toLocaleString()} source bytes</p>}<div className="mt-4 rounded-lg border border-slate-800 p-4"><h3 className="font-medium">Objective assessment</h3><p className="mt-2 text-sm text-slate-300">{analysis.structured_payload.objective_assessment.answer}</p><p className="mt-2 text-xs text-slate-500">{analysis.structured_payload.objective_assessment.confidence} confidence</p>{analysis.structured_payload.objective_assessment.limitations.length>0&&<ul className="mt-2 list-disc pl-5 text-xs text-slate-500">{analysis.structured_payload.objective_assessment.limitations.map(item=><li key={item}>{item}</li>)}</ul>}</div><p className="mt-4 text-slate-300">{analysis.structured_payload.summary}</p><div className="mt-6 space-y-4">{analysis.structured_payload.findings.map((finding,index)=><article key={`${finding.title}-${index}`} className="rounded-lg border border-slate-800 p-4"><div className="flex gap-3 text-xs uppercase text-slate-400"><span>{finding.category}</span><span>{finding.severity}</span><span>{finding.confidence} confidence</span></div><h3 className="mt-2 font-medium">{finding.title}</h3><p className="mt-2 text-sm text-slate-300">{finding.description}</p><div className="mt-3 text-xs text-slate-500">{finding.evidence.map(e=>{const source=analysis.evidence.find(item=>item.id===e.source_reference_id);return <span key={e.source_reference_id} className="mr-3">{source ? `${source.path} · lines ${source.start_line}–${source.end_line}` : "Evidence unavailable"}</span>})}</div></article>)}</div></section>}
    {approval && <section className="rounded-xl border p-6"><h2 className="text-xl font-semibold">{approval.status === "pending" ? "Human approval required" : "Approval decision"}</h2><p className="mt-2 text-sm text-slate-400">{approval.approval_type === "proceed_to_analysis" ? "Approval authorizes bounded, read-only AI repository analysis." : "Approval confirms the analysis was reviewed and authorizes a future planning phase. No planning is dispatched in Phase 5."}</p>{approval.status === "pending" ? <div className="mt-5"><textarea value={comment} onChange={(event) => setComment(event.target.value)} maxLength={1000} placeholder="Optional decision comment" className="w-full rounded-md border bg-slate-950 p-3"/><div className="mt-3 flex gap-3"><button onClick={() => mutate(`approvals/${approval.id}/approve`, { comment })} className="rounded-md bg-emerald-500 px-4 py-2 text-slate-950">Approve</button><button onClick={() => mutate(`approvals/${approval.id}/reject`, { comment })} className="rounded-md border border-red-800 px-4 py-2 text-red-300">Reject</button></div></div> : <p className="mt-4 capitalize">{approval.status}{approval.decision_comment ? `: ${approval.decision_comment}` : ""}</p>}</section>}
  </div>;
}
