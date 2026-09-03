import Link from "next/link";
import { redirect } from "next/navigation";
import { AuthForm } from "@/components/auth-form";
import { currentActor } from "@/lib/server-api";

export default async function LoginPage(){if(await currentActor())redirect("/app");return <AuthPage title="Welcome back" footer={<>New to DevPilot? <Link className="text-emerald-400" href="/register">Create an account</Link>.</>}><AuthForm mode="login"/></AuthPage>}
function AuthPage({title,children,footer}:{title:string;children:React.ReactNode;footer:React.ReactNode}){return <main className="mx-auto flex min-h-screen max-w-md flex-col justify-center px-6"><Link className="mb-10 text-lg font-semibold" href="/">DevPilot</Link><h1 className="text-3xl font-semibold">{title}</h1>{children}<p className="mt-6 text-sm text-slate-400">{footer}</p></main>}
