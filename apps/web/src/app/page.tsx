const foundations = [
  "Human approval before implementation and pull-request creation",
  "Inspectable plans, diffs, review findings, and execution evidence",
  "A separate, resource-bounded sandbox trust boundary",
  "Auditable platform state owned by the Go API and PostgreSQL",
];

export default function Home() {
  return (
    <main className="mx-auto flex min-h-screen max-w-6xl flex-col px-6 py-10 sm:px-10">
      <header className="flex items-center justify-between border-b pb-6">
        <span className="text-lg font-semibold tracking-tight">DevPilot</span>
        <span className="rounded-full border px-3 py-1 text-xs text-slate-400">Phase 0</span>
      </header>

      <section className="grid flex-1 items-center gap-14 py-20 lg:grid-cols-[1.3fr_0.7fr]">
        <div>
          <p className="mb-5 font-mono text-sm uppercase tracking-[0.22em] text-emerald-400">
            Human-supervised engineering
          </p>
          <h1 className="max-w-3xl text-5xl font-semibold tracking-tight sm:text-7xl">
            Software agents with deliberate boundaries.
          </h1>
          <p className="mt-7 max-w-2xl text-lg leading-8 text-slate-300">
            DevPilot is being built to analyze repositories, propose plans, execute approved work
            in isolation, validate changes, and prepare reviewable pull requests.
          </p>
          <p className="mt-5 text-sm text-slate-500">
            The product workflow is planned. This repository currently contains the architecture
            and application foundations only.
          </p>
        </div>

        <aside className="rounded-2xl border bg-slate-950/60 p-6 shadow-2xl shadow-emerald-950/20">
          <p className="text-sm font-medium text-slate-200">Foundations defined</p>
          <ul className="mt-5 space-y-4">
            {foundations.map((foundation) => (
              <li className="flex gap-3 text-sm leading-6 text-slate-400" key={foundation}>
                <span aria-hidden="true" className="mt-2 h-1.5 w-1.5 shrink-0 rounded-full bg-emerald-400" />
                {foundation}
              </li>
            ))}
          </ul>
        </aside>
      </section>
    </main>
  );
}
