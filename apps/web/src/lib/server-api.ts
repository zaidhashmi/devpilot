import "server-only";

import { cookies } from "next/headers";

import type { Actor, EngineeringTask, GitHubInstallation, Repository, TaskRun, Workspace } from "@/lib/types";

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
export async function workspace(id:string):Promise<Workspace|null>{const response=await authenticatedFetch(`/api/v1/workspaces/${encodeURIComponent(id)}`);if(!response?.ok)return null;return await response.json() as Workspace;}
export async function tasks():Promise<EngineeringTask[]>{const response=await authenticatedFetch("/api/v1/tasks");if(!response?.ok)return[];return((await response.json())as{tasks:EngineeringTask[]}).tasks}
export async function task(id:string):Promise<{task:EngineeringTask;runs:TaskRun[]}|null>{const response=await authenticatedFetch(`/api/v1/tasks/${encodeURIComponent(id)}`);if(!response?.ok)return null;return await response.json() as {task:EngineeringTask;runs:TaskRun[]}}
export async function taskRun(id:string):Promise<TaskRun|null>{const response=await authenticatedFetch(`/api/v1/task-runs/${encodeURIComponent(id)}`);if(!response?.ok)return null;return await response.json() as TaskRun}
