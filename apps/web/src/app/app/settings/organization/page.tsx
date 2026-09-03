import { OrganizationForm } from "@/components/organization-form";
import { currentActor } from "@/lib/server-api";

export default async function OrganizationSettingsPage(){const actor=await currentActor();const canManage=actor?.membership.role==="owner"||actor?.membership.role==="admin";return <main className="py-16"><h1 className="text-3xl font-semibold">Organization settings</h1><p className="mt-2 text-slate-400">Slug: {actor?.organization.slug}</p>{actor&&<OrganizationForm canManage={canManage} initialName={actor.organization.name}/>}</main>}
