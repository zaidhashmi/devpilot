"use client";

import { FormEvent, useState } from "react";
import { useRouter } from "next/navigation";

type Mode = "login" | "register";

export function AuthForm({ mode }: { mode: Mode }) {
  const router = useRouter();
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setPending(true); setError("");
    const values = Object.fromEntries(new FormData(event.currentTarget));
    const response = await fetch(`/backend/api/v1/auth/${mode}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(values),
    });
    if (!response.ok) {
      const result = (await response.json().catch(() => null)) as { error?: { message?: string } } | null;
      setError(result?.error?.message ?? "The request could not be completed."); setPending(false); return;
    }
    router.replace("/app"); router.refresh();
  }

  return (
    <form className="mt-8 space-y-5" onSubmit={submit}>
      {mode === "register" && <>
        <Field label="Display name" name="display_name" autoComplete="name" />
        <Field label="Organization name" name="organization_name" autoComplete="organization" />
      </>}
      <Field label="Email" name="email" type="email" autoComplete="email" />
      <Field label="Password" name="password" type="password" autoComplete={mode === "login" ? "current-password" : "new-password"} minLength={12} />
      {error && <p className="rounded-lg border border-red-900 bg-red-950/40 p-3 text-sm text-red-300" role="alert">{error}</p>}
      <button className="w-full rounded-lg bg-emerald-400 px-4 py-3 font-medium text-slate-950 disabled:opacity-60" disabled={pending} type="submit">
        {pending ? "Working…" : mode === "login" ? "Log in" : "Create account"}
      </button>
    </form>
  );
}

function Field(props: { label: string; name: string; type?: string; autoComplete: string; minLength?: number }) {
  return <label className="block text-sm text-slate-300">{props.label}<input {...props} required className="mt-2 w-full rounded-lg border bg-slate-950 px-3 py-2.5 text-slate-100 outline-none focus:border-emerald-500" /></label>;
}
