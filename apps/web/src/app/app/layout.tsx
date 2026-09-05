import Link from "next/link";
import { redirect } from "next/navigation";
import { LogoutButton } from "@/components/logout-button";
import { currentActor } from "@/lib/server-api";

export default async function AppLayout({children}:{children:React.ReactNode}){const actor=await currentActor();if(!actor)redirect("/login");return <div className="mx-auto min-h-screen max-w-6xl px-6 py-8"><header className="flex items-center justify-between border-b pb-5"><div className="flex items-center gap-8"><Link className="font-semibold" href="/app">DevPilot</Link><Link className="text-sm text-slate-400 hover:text-white" href="/app/repositories">Repositories</Link><Link className="text-sm text-slate-400 hover:text-white" href="/app/settings/integrations/github">GitHub</Link><Link className="text-sm text-slate-400 hover:text-white" href="/app/settings/organization">Organization settings</Link></div><LogoutButton/></header>{children}</div>}
