import "server-only";

import { cookies } from "next/headers";

import type { Actor } from "@/lib/types";

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
