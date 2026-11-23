"use client";

import { useEffect, useState, type CSSProperties } from "react";

export type Flow = {
  label: string;
  title: string;
  body: string;
  stats: { value: string; label: string }[];
  entries: { account: string; side: "Dr" | "Cr"; amount: string }[];
};

const DURATION = 6500;

/** Auto-advancing tabs: a progress line fills under the active tab. */
export function FlowTabs({ flows }: { flows: Flow[] }) {
  const [active, setActive] = useState(0);
  const [paused, setPaused] = useState(false);

  useEffect(() => {
    if (paused || window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    const t = setTimeout(() => setActive((a) => (a + 1) % flows.length), DURATION);
    return () => clearTimeout(t);
  }, [active, paused, flows.length]);

  const f = flows[active];
  return (
    <div onMouseEnter={() => setPaused(true)} onMouseLeave={() => setPaused(false)}>
      <div role="tablist" className="no-scrollbar grid auto-cols-[minmax(160px,1fr)] grid-flow-col gap-6 overflow-x-auto">
        {flows.map((x, i) => (
          <button
            key={x.label}
            role="tab"
            aria-selected={i === active}
            onClick={() => setActive(i)}
            className={`relative pb-4 text-left text-label transition-colors ${i === active ? "text-ink" : "text-slate hover:text-ink"}`}
          >
            {x.label}
            <span className="absolute inset-x-0 bottom-0 h-px bg-mist" />
            {i === active && (
              <span key={`${active}-${paused}`} className={`absolute inset-x-0 bottom-0 h-0.5 bg-ink ${paused ? "" : "tab-progress"}`} style={{ "--tab-duration": `${DURATION}ms` } as CSSProperties} />
            )}
          </button>
        ))}
      </div>

      <div key={active} role="tabpanel" className="band relative mt-8 grid gap-8 overflow-hidden rounded-large p-8 md:grid-cols-[1.1fr_1fr] md:p-14">
        <div className="halo" style={{ left: "85%", top: "20%", width: "60%" }} />
        <div className="relative">
          <h3 className="text-heading md:text-heading-lg" style={{ animation: "fadein 1.2s var(--ease-out-expo) both" }}>
            {f.title}
          </h3>
          <p className="mt-6 max-w-md text-subheading text-fog">{f.body}</p>
          <div className="mt-12 flex flex-wrap gap-10">
            {f.stats.map((s) => (
              <div key={s.label}>
                <div className="text-display tracking-display">{s.value}</div>
                <div className="mt-3 text-caption uppercase text-fog">{s.label}</div>
              </div>
            ))}
          </div>
        </div>
        <div className="relative self-center rounded-card bg-white p-6 text-ink">
          <div className="flex items-center justify-between text-caption uppercase text-warm-gray">
            <span>Ledger transaction</span>
            <span className="inline-flex items-center gap-1.5"><span className="h-1.5 w-1.5 rounded-full bg-pulse" />balanced</span>
          </div>
          <table className="mt-4 w-full text-label">
            <tbody className="divide-y divide-mist">
              {f.entries.map((e, i) => (
                <tr key={e.account} className="row-in" style={{ "--i": i } as CSSProperties}>
                  <td className="py-3 font-mono text-[13px]">{e.account}</td>
                  <td className="py-3 text-warm-gray">{e.side}</td>
                  <td className="py-3 text-right tabular-nums">{e.amount}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <p className="mt-4 border-t border-ink pt-3 text-caption uppercase text-warm-gray">Debits = credits · append-only</p>
        </div>
      </div>
    </div>
  );
}
