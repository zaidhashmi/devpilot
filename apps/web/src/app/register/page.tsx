import Link from "next/link";
import { redirect } from "next/navigation";
import { AuthForm } from "@/components/auth-form";
import { currentActor } from "@/lib/server-api";

export default async function RegisterPage(){if(await currentActor())redirect("/app");return <main className="mx-auto flex min-h-screen max-w-md flex-col justify-center px-6 py-12"><Link className="mb-10 text-lg font-semibold" href="/">DevPilot</Link><h1 className="text-3xl font-semibold">Create your workspace</h1><p className="mt-2 text-sm text-slate-400">Registration creates your user, organization, and owner membership atomically.</p><AuthForm mode="register"/><p className="mt-6 text-sm text-slate-400">Already registered? <Link className="text-emerald-400" href="/login">Log in</Link>.</p></main>}
