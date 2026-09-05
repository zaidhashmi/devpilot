import "server-only";

import { cookies } from "next/headers";

import type { Actor, GitHubInstallation, Repository } from "@/lib/types";

const apiURL = process.env.DEVPILOT_INTERNAL_API_URL ?? "http://127.0.0.1:8080";

export async function currentActor(): Promise<Actor | null> {
  const cookieStore = await cookies();
  try {
    const response = await fetch(`${apiURL}/api/v1/me`, {
      cache: "no-store",
      headers: { cookie: cookieStore.toString() },
    });
    if (!response.ok) return null;
    return (await response.json()) as Actor;
  } catch {
    return null;
  }
}

async function authenticatedFetch(path: string): Promise<Response | null> {
  const cookieStore = await cookies();
  try { return await fetch(`${apiURL}${path}`, { cache: "no-store", headers: { cookie: cookieStore.toString() } }); }
  catch { return null; }
}

export async function githubIntegration(): Promise<{enabled:boolean;installation:GitHubInstallation|null}> {
  const response=await authenticatedFetch("/api/v1/integrations/github");
  if(!response?.ok)return {enabled:false,installation:null};
  return await response.json() as {enabled:boolean;installation:GitHubInstallation|null};
}

export async function repositories(): Promise<Repository[]> {
  const response=await authenticatedFetch("/api/v1/repositories");
  if(!response?.ok)return [];
  return ((await response.json()) as {repositories:Repository[]}).repositories;
}
