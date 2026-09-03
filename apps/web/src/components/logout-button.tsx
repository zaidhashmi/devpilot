"use client";

import { useRouter } from "next/navigation";

function csrfToken(): string {
  return document.cookie.split("; ").find((part) => part.startsWith("devpilot_csrf="))?.split("=")[1] ?? "";
}

export function LogoutButton() {
  const router = useRouter();
  return <button className="text-sm text-slate-400 hover:text-white" onClick={async () => {
    await fetch("/backend/api/v1/auth/logout", { method: "POST", headers: { "X-CSRF-Token": csrfToken() } });
    router.replace("/login"); router.refresh();
  }}>Log out</button>;
}

export { csrfToken };
