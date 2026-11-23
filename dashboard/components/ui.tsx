import Link from "next/link";
import type { ReactNode } from "react";

// Primitives for the FinCore design system (DESIGN.md): white canvas, black
// structure, one neon-pink pulse, weight 400 only, flat surfaces.

export function Container({ children, className = "" }: { children: ReactNode; className?: string }) {
  return <div className={`mx-auto w-full max-w-[1200px] px-4 md:px-6 ${className}`}>{children}</div>;
}

/** Page title: compact, console-style. One key word may be wrapped in <Pulse>. */
export function PageHeader({ eyebrow, title, description, actions }: { eyebrow?: string; title: ReactNode; description?: string; actions?: ReactNode }) {
  return (
    <header className="flex flex-wrap items-end justify-between gap-4 pt-8 pb-8">
      <div className="min-w-0 max-w-3xl">
        {eyebrow && <div className="mb-2 text-caption uppercase text-warm-gray">{eyebrow}</div>}
        <h1 className="text-heading">{title}</h1>
        {description && <p className="mt-2 max-w-2xl text-data text-warm-gray">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-3">{actions}</div>}
    </header>
  );
}

/** A highlighted word inside a headline — the only pink type in the system. */
export function Pulse({ children }: { children: ReactNode }) {
  return <span className="text-pulse">{children}</span>;
}

/** Brand badge: transparent, pink hairline, 100px pill, wide-tracked caption. */
export function Tag({ children }: { children: ReactNode }) {
  return <span className="inline-flex items-center rounded-tag border border-pulse px-4 py-1 text-caption uppercase">{children}</span>;
}

export function Card({ title, children, className = "", actions, flush }: { title?: string; children: ReactNode; className?: string; actions?: ReactNode; flush?: boolean }) {
  return (
    <section className={`rounded-card border border-mist bg-white ${className}`}>
      {title && (
        <header className="flex flex-wrap items-center justify-between gap-3 px-6 pt-5 pb-4">
          <h2 className="text-data">{title}</h2>
          {actions}
        </header>
      )}
      <div className={flush ? "" : title ? "px-6 pb-6" : "p-6"}>{children}</div>
    </section>
  );
}

/** Full-bleed black band — the system's only tonal break. */
export function DarkBand({ children, title }: { children: ReactNode; title?: string }) {
  return (
    <section className="band relative left-1/2 my-20 w-screen -translate-x-1/2 py-20">
      <Container>
        {title && <h2 className="mb-10 text-caption uppercase text-fog">{title}</h2>}
        {children}
      </Container>
    </section>
  );
}

export function DarkCard({ label, value, note, alert }: { label: string; value: ReactNode; note?: string; alert?: boolean }) {
  return (
    <div className="rounded-card bg-band-card p-6 ring-1 ring-white/10">
      <div className="flex items-center gap-2 text-caption uppercase text-fog">
        {alert && <span className="h-2 w-2 rounded-full bg-pulse" aria-hidden />}
        {label}
      </div>
      <div className="mt-6 text-display tabular-nums">{value}</div>
      {note && <p className="mt-4 text-data text-fog">{note}</p>}
    </div>
  );
}

/** The poster numeral: one per page. Fluid so long ₹ amounts still fit. */
export function StatHero({ label, value, note }: { label: string; value: ReactNode; note?: string }) {
  return (
    <div className="border-b border-mist pb-8">
      <div className="text-[clamp(44px,7vw,88px)] leading-[0.9] tracking-display tabular-nums">{value}</div>
      <p className="mt-4 text-label text-warm-gray">
        {label}
        {note && <span> · {note}</span>}
      </p>
    </div>
  );
}

export function Stat({ label, value, hint, alert }: { label: string; value: ReactNode; hint?: string; alert?: boolean }) {
  return (
    <div>
      <div className="flex items-center gap-2 text-caption uppercase text-warm-gray">
        {alert && <span className="h-2 w-2 rounded-full bg-pulse" aria-hidden />}
        {label}
      </div>
      <div className="mt-3 text-heading md:text-heading-lg tabular-nums">{value}</div>
      {hint && <p className="mt-2 text-data text-warm-gray">{hint}</p>}
    </div>
  );
}

export function StatRow({ children }: { children: ReactNode }) {
  return <div className="grid grid-cols-2 gap-x-6 gap-y-10 py-12 lg:grid-cols-4">{children}</div>;
}

// Status is expressed with fill and outline, never a second hue:
//   settled → solid black · moving → black outline · neutral → mist outline
//   needs attention → pink outline with a pink dot
type Tone = "settled" | "moving" | "neutral" | "attention";

const toneClass: Record<Tone, string> = {
  settled: "bg-ink text-white border-ink",
  moving: "border-ink text-ink",
  neutral: "border-mist text-slate",
  attention: "border-pulse text-ink",
};

const statusTone: Record<string, Tone> = {
  captured: "settled",
  succeeded: "settled",
  paid: "settled",
  delivered: "settled",
  matched: "settled",
  balanced: "settled",
  active: "settled",
  authorized: "moving",
  pending: "moving",
  created: "moving",
  partially_refunded: "moving",
  refunded: "neutral",
  canceled: "neutral",
  skipped: "neutral",
  revoked: "neutral",
  duplicate: "neutral",
  unsupported: "attention",
  failed: "attention",
  dead: "attention",
  unbalanced: "attention",
  missing_in_ledger: "attention",
  amount_mismatch: "attention",
  status_mismatch: "attention",
};

export function Badge({ children, tone }: { children: string; tone?: Tone }) {
  const t = tone ?? statusTone[children] ?? "neutral";
  return (
    <span className={`inline-flex items-center gap-1.5 whitespace-nowrap rounded-tag border px-2.5 py-0.5 text-caption uppercase leading-5 ${toneClass[t]}`}>
      {t === "attention" && <span className="h-1.5 w-1.5 rounded-full bg-pulse" aria-hidden />}
      {children.replaceAll("_", " ")}
    </span>
  );
}

const numericHeads = new Set(["Amount", "Fee", "Balance", "Debits", "Credits", "Captured", "Refunded", "Attempts", "Tax", "Lines", "Matched", "Exceptions", "Unsettled", "Net to bank", "Pending payouts", "Line"]);

export function Table({ head, children, empty }: { head: string[]; children: ReactNode; empty?: boolean }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left text-data">
        <thead>
          <tr className="border-b border-ink">
            {head.map((h, i) => (
              <th key={h + i} className={`whitespace-nowrap px-3 pb-3 text-caption uppercase text-warm-gray first:pl-0 last:pr-0 ${numericHeads.has(h) ? "text-right" : ""}`}>
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-mist">{children}</tbody>
      </table>
      {empty && <p className="py-10 text-center text-data text-warm-gray">Nothing here yet.</p>}
    </div>
  );
}

export function Td({ children, numeric, mono, muted, className = "" }: { children: ReactNode; numeric?: boolean; mono?: boolean; muted?: boolean; className?: string }) {
  return (
    <td
      className={`whitespace-nowrap px-3 py-3.5 first:pl-0 last:pr-0 ${numeric ? "text-right tabular-nums" : ""} ${mono ? "font-mono text-[13px]" : ""} ${muted ? "text-warm-gray" : ""} ${className}`}
    >
      {children}
    </td>
  );
}

/** Inline link: black text, mist underline that turns pink on hover. */
export function TextLink({ href, children, className = "" }: { href: string; children: ReactNode; className?: string }) {
  return (
    <Link href={href} className={`underline decoration-mist underline-offset-4 transition-colors hover:decoration-pulse ${className}`}>
      {children}
    </Link>
  );
}

export function IdLink({ href, id }: { href: string; id: string }) {
  return (
    <TextLink href={href} className="font-mono text-[13px]">
      {id.slice(0, 8)}
    </TextLink>
  );
}

export function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <dt className="text-caption uppercase text-warm-gray">{label}</dt>
      <dd className="mt-1 text-body">{children}</dd>
    </div>
  );
}

/** Link pills (24px radius); the active one is filled black. */
export function Tabs({ items, active }: { items: { label: string; href: string; value: string }[]; active: string }) {
  return (
    <nav className="mb-8 flex flex-wrap gap-2">
      {items.map((t) => (
        <Link
          key={t.value}
          href={t.href}
          className={`rounded-link border px-4 py-1.5 text-data capitalize transition-colors ${
            t.value === active ? "border-ink bg-ink text-white" : "border-mist text-slate hover:border-ink hover:text-ink"
          }`}
        >
          {t.label}
        </Link>
      ))}
    </nav>
  );
}

export function Skeleton({ rows = 5 }: { rows?: number }) {
  return (
    <div className="animate-pulse space-y-4 py-2" aria-label="Loading">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="h-3 rounded-tag bg-mist" style={{ width: `${100 - ((i * 13) % 40)}%` }} />
      ))}
    </div>
  );
}

export const buttonClass =
  "inline-flex items-center justify-center whitespace-nowrap rounded-pill bg-ink px-6 py-4 text-body text-white transition-opacity hover:opacity-80 disabled:opacity-40";

export const ghostButtonClass =
  "inline-flex items-center justify-center whitespace-nowrap rounded-pill border border-ink px-6 py-4 text-body text-ink transition-colors hover:bg-ink hover:text-white disabled:opacity-40";

export const smallButtonClass =
  "inline-flex items-center rounded-pill border border-ink px-3 py-1 text-caption uppercase transition-colors hover:bg-ink hover:text-white";

export const inputClass =
  "w-full rounded-card border border-mist bg-white px-4 py-4 text-body text-ink placeholder:text-fog focus:border-ink focus:outline-none";
