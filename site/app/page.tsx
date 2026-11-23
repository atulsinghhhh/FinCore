import type { CSSProperties, ReactNode } from "react";
import { Footer, Header } from "@/components/chrome";
import { Counter } from "@/components/counter";
import { FlowTabs, type Flow } from "@/components/flow-tabs";
import { heroCards } from "@/components/mockups";
import { Motion } from "@/components/motion";
import { Slider } from "@/components/slider";
import { Title } from "@/components/title";
import { API_URL, DASHBOARD_URL } from "@/lib/links";

const Wrap = ({ children, className = "" }: { children: ReactNode; className?: string }) => (
  <div className={`mx-auto w-full max-w-[1200px] px-4 md:px-6 ${className}`}>{children}</div>
);

const Fade = ({ children, delay = 0, className = "" }: { children: ReactNode; delay?: number; className?: string }) => (
  <div className={`will-animate fade ${className}`} style={{ "--delay": `${delay}ms` } as CSSProperties}>{children}</div>
);

function Sparkle({ className = "", size = 28 }: { className?: string; size?: number }) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} className={className} aria-hidden>
      <path d="M12 0c.6 6.6 5.4 11.4 12 12-6.6.6-11.4 5.4-12 12-.6-6.6-5.4-11.4-12-12C6.6 11.4 11.4 6.6 12 0Z" fill="currentColor" />
    </svg>
  );
}

const products = [
  { name: "Payments", line: "Authorize, capture, or both at once.", detail: "Sandbox cards and Razorpay Checkout behind one API. Partial and manual capture included.", stat: "₹1,000", statLabel: "captured in one call" },
  { name: "Refunds", line: "Full or partial, fee returned pro rata.", detail: "Rounded so the pieces always sum to the original fee — to the paisa.", stat: "₹4.00", statLabel: "fee returned on ₹200" },
  { name: "Payouts", line: "Available balance, straight to the bank.", detail: "Held in transit until the bank confirms. Rejections reverse themselves.", stat: "3", statLabel: "states, never skipped" },
  { name: "Reconciliation", line: "Your processor's file, checked line by line.", detail: "Matches move money to the bank; mismatches become exceptions, not surprises.", stat: "100%", statLabel: "of lines matched or flagged" },
  { name: "Webhooks", line: "Signed events from a transactional outbox.", detail: "Written in the same transaction as the change. Retried with backoff. Never phantom.", stat: "12", statLabel: "retries before dead-letter" },
];

const stack = ["Go", "PostgreSQL 17", "Dragonfly", "Razorpay", "UPI", "RuPay", "OpenTelemetry", "Prometheus", "Grafana", "sqlc", "pgx", "Next.js", "Docker", "k6"];

const principles = [
  ["01", "Money is never created or destroyed. It only moves.", "Every transaction is double-entry. The database refuses to commit one where debits and credits differ."],
  ["02", "History is never rewritten.", "Ledger rows are append-only — UPDATE and DELETE raise errors. Mistakes are corrected with reversals."],
  ["03", "A retry is never a second charge.", "Idempotency keys replay the original response from Dragonfly, backed by unique keys in Postgres."],
];

const flows: Flow[] = [
  {
    label: "Capture", title: "A payment, booked the moment it lands.",
    body: "The customer pays ₹1,000. The merchant's balance and the platform fee are recorded in one atomic, balanced entry.",
    stats: [{ value: "1", label: "atomic transaction" }, { value: "2%", label: "platform fee" }],
    entries: [{ account: "psp_clearing", side: "Dr", amount: "₹1,000.00" }, { account: "merchant:payable", side: "Cr", amount: "₹980.00" }, { account: "fee_revenue", side: "Cr", amount: "₹20.00" }],
  },
  {
    label: "Refund", title: "Refunds that never overdraw.",
    body: "The ledger checks the merchant's balance before the processor is called, so a refund can't send money the merchant no longer has.",
    stats: [{ value: "₹200", label: "refunded" }, { value: "₹4", label: "fee returned" }],
    entries: [{ account: "merchant:payable", side: "Dr", amount: "₹196.00" }, { account: "fee_revenue", side: "Dr", amount: "₹4.00" }, { account: "psp_clearing", side: "Cr", amount: "₹200.00" }],
  },
  {
    label: "Payout", title: "Paid out, provably.",
    body: "Funds move to an in-transit account until the bank confirms. If the bank rejects, the hold reverses and the balance comes back.",
    stats: [{ value: "₹784", label: "paid out" }, { value: "0", label: "negative balances" }],
    entries: [{ account: "merchant:payable", side: "Dr", amount: "₹784.00" }, { account: "payouts_in_transit", side: "Cr", amount: "₹784.00" }],
  },
  {
    label: "Settle", title: "Every settlement line, matched.",
    body: "Import the processor's settlement file. Matched lines move cash to the bank net of fees; anything else is flagged for review.",
    stats: [{ value: "2/2", label: "lines matched" }, { value: "₹88", label: "processor fees" }],
    entries: [{ account: "bank", side: "Dr", amount: "₹3,660.52" }, { account: "processor_fees", side: "Dr", amount: "₹88.48" }, { account: "psp_clearing", side: "Cr", amount: "₹3,749.00" }],
  },
];

export default function Home() {
  return (
    <>
      <Header />
      <main>
        {/* Hero */}
        <section className="pt-20 text-center md:pt-28">
          <Wrap>
            <Fade>
              <span className="inline-flex items-center gap-2 rounded-tag border border-mist px-4 py-1.5 text-label text-warm-gray">
                <Sparkle size={12} className="spark text-pulse" /> Built on a double-entry ledger
              </span>
            </Fade>
            <Title as="h1" text="Payments that always" strong="add up." className="mx-auto mt-8 max-w-4xl text-[clamp(48px,8vw,96px)] leading-[0.95] tracking-display" />
            <Fade delay={500}>
              <p className="mx-auto mt-8 max-w-xl text-subheading text-warm-gray">
                Payments, refunds, payouts and reconciliation on a ledger the database itself keeps balanced. Retries never double-charge. History is never rewritten.
              </p>
            </Fade>
            <Fade delay={700} className="mt-10 flex flex-wrap justify-center gap-3">
              <a href={DASHBOARD_URL} className="rounded-pill bg-ink px-6 py-4 text-body text-white transition-opacity hover:opacity-80">Open the dashboard</a>
              <a href={`${API_URL}/openapi.yaml`} className="group rounded-pill border border-mist px-6 py-4 text-body transition-colors hover:border-ink">
                Read the API <span className="inline-block transition-transform group-hover:translate-x-1">→</span>
              </a>
            </Fade>
          </Wrap>
          <Fade delay={900} className="marquee-host relative mt-20 overflow-hidden [mask-image:linear-gradient(to_right,transparent,black_6%,black_94%,transparent)]">
            <div className="marquee gap-4 pr-4" style={{ "--duration": "70s" } as CSSProperties}>
              {[...heroCards, ...heroCards].map((Card, i) => <Card key={i} />)}
            </div>
          </Fade>
        </section>

        {/* Products, dark band */}
        <section id="product" className="band mt-28 scroll-mt-20 py-24 md:py-32">
          <Wrap>
            <Title dark text="Built for money" strong="that moves." className="mb-14 max-w-3xl text-heading md:text-heading-lg" />
            <Slider label="FinCore products">
              {products.map((p, i) => (
                <article key={p.name} className="will-animate fade relative flex h-[460px] w-[300px] shrink-0 snap-start flex-col overflow-hidden rounded-card bg-band-card p-7 md:w-[340px]" style={{ "--delay": `${i * 90}ms` } as CSSProperties}>
                  <h3 className="text-heading-sm">{p.name}</h3>
                  <p className="mt-2 text-label text-fog">{p.line}</p>
                  <div className="mt-auto">
                    <div className="text-display tracking-display">{p.stat}</div>
                    <div className="mt-2 text-caption uppercase text-fog">{p.statLabel}</div>
                    <p className="mt-6 border-t border-white/15 pt-4 text-label text-fog">{p.detail}</p>
                  </div>
                </article>
              ))}
              <article className="will-animate fade relative flex h-[460px] w-[300px] shrink-0 snap-start flex-col overflow-hidden rounded-card bg-band-card p-7 md:w-[340px]" style={{ "--delay": "450ms" } as CSSProperties}>
                <div className="halo" style={{ top: "60%", width: "110%" }} />
                <Sparkle className="wiggle relative text-pulse" size={34} />
                <h3 className="relative mt-6 text-heading">Powered by the ledger</h3>
                <p className="relative mt-3 text-label text-fog">Every product above writes balanced, immutable entries — so every number on every screen can be traced to its source.</p>
                <a href="#ledger" className="relative mt-auto inline-flex w-fit rounded-pill bg-white px-5 py-2.5 text-label text-ink">See how →</a>
              </article>
            </Slider>
          </Wrap>
        </section>

        {/* Stack marquee */}
        <section className="py-24">
          <Wrap>
            <Title text="Built on boring," strong="battle-tested" after="infrastructure." className="mx-auto mb-14 max-w-3xl text-center text-heading md:text-heading-lg" />
          </Wrap>
          <div className="marquee-host space-y-6 overflow-hidden [mask-image:linear-gradient(to_right,transparent,black_10%,black_90%,transparent)]">
            {[false, true].map((rev) => (
              <div key={String(rev)} className={`marquee ${rev ? "marquee-reverse" : ""}`} style={{ "--duration": rev ? "55s" : "45s" } as CSSProperties}>
                {[...stack, ...stack].map((s, i) => (
                  <span key={i} className="flex items-center gap-10 pr-10 text-heading text-slate">
                    {s}
                    <Sparkle size={14} className="text-mist" />
                  </span>
                ))}
              </div>
            ))}
          </div>
        </section>

        {/* Principles (editorial) */}
        <section className="border-t border-mist py-24 md:py-32">
          <Wrap>
            <div className="flex flex-wrap items-end justify-between gap-6">
              <Title text="Rules the ledger" strong="never" after="breaks." className="max-w-2xl text-heading md:text-heading-lg" />
              <Fade><a href={`${API_URL}/openapi.yaml`} className="rounded-pill border border-ink px-5 py-2.5 text-label transition-colors hover:bg-ink hover:text-white">Read the spec</a></Fade>
            </div>
            <div className="will-animate mt-16 grid gap-10 md:grid-cols-3">
              {principles.map(([n, head, body], i) => (
                <article key={n} className="fade-child border-t border-ink pt-6" style={{ "--i": i } as CSSProperties}>
                  <div className="flex items-center gap-2 text-caption uppercase"><span className="h-1.5 w-1.5 rounded-full bg-pulse" aria-hidden />{n}</div>
                  <p className="mt-6 text-heading-sm md:text-heading">“{head}”</p>
                  <p className="mt-6 text-body text-warm-gray">{body}</p>
                </article>
              ))}
            </div>
          </Wrap>
        </section>

        {/* Bento, dark band */}
        <section id="ledger" className="band scroll-mt-20 py-24 md:py-32">
          <Wrap>
            <Title dark text="One ledger. Three teams." strong="Everyone" after="reconciles." className="mb-14 max-w-3xl text-heading md:text-heading-lg" />
            <div className="will-animate grid gap-4 md:grid-cols-3">
              {[
                { who: "For developers", what: "Idempotent APIs", stats: [["0", "double charges"], ["173ms", "p95 under load"]] },
                { who: "For finance", what: "Settlement reconciliation", stats: [["100%", "lines checked"], ["3d", "unsettled alert"]] },
                { who: "For operations", what: "Outbox & integrity checks", stats: [["5 min", "ledger re-verified"], ["12×", "webhook retries"]] },
              ].map((c, i) => (
                <article key={c.who} className="fade-child relative flex min-h-[440px] flex-col overflow-hidden rounded-card bg-band-card p-8" style={{ "--i": i } as CSSProperties}>
                  <div className="halo" style={{ top: "15%", left: `${30 + i * 25}%`, width: "90%", animationDelay: `${-i * 4}s` }} />
                  <Sparkle className="float absolute right-8 top-8 text-pulse" size={22} />
                  <div className="relative text-caption uppercase text-fog">{c.who}</div>
                  <h3 className="relative mt-2 text-heading">{c.what}</h3>
                  <div className="relative mt-auto flex flex-wrap gap-x-8 gap-y-4">
                    {c.stats.map(([v, l]) => (
                      <div key={l}>
                        <div className="text-display tracking-display">{v}</div>
                        <div className="mt-2 text-label text-fog">{l}</div>
                      </div>
                    ))}
                  </div>
                </article>
              ))}
            </div>
          </Wrap>
        </section>

        {/* Flows */}
        <section id="flows" className="scroll-mt-20 py-24 md:py-32">
          <Wrap>
            <Title text="You move the money." strong="We" after="keep the books." className="mb-14 max-w-3xl text-heading md:text-heading-lg" />
            <Fade><FlowTabs flows={flows} /></Fade>
          </Wrap>
        </section>

        {/* Developers */}
        <section id="developers" className="scroll-mt-20 border-t border-mist py-24 md:py-32">
          <Wrap className="grid items-center gap-14 lg:grid-cols-2">
            <div>
              <Title text="An API that's" strong="safe" after="to retry." className="text-heading md:text-heading-lg" />
              <ul className="will-animate mt-10 space-y-5">
                {[
                  ["Idempotency-Key on every write", "Same key, same response — replayed byte for byte."],
                  ["Signed webhooks", "HMAC with timestamp; verify in five lines."],
                  ["Clear errors", "insufficient_funds, invalid_state, idempotency_key_reused."],
                  ["OpenAPI 3.1", "Every route documented — a test fails if one isn't."],
                ].map(([h, b], i) => (
                  <li key={h} className="fade-child flex gap-4 border-t border-mist pt-5" style={{ "--i": i } as CSSProperties}>
                    <span className="mt-2 h-2 w-2 shrink-0 rounded-full bg-pulse" />
                    <div><div className="text-body">{h}</div><div className="mt-1 text-label text-warm-gray">{b}</div></div>
                  </li>
                ))}
              </ul>
            </div>
            <Fade className="band overflow-hidden rounded-card p-6 font-mono text-[13px] leading-relaxed text-fog md:p-8">
              <div className="mb-5 flex gap-2"><span className="h-3 w-3 rounded-full bg-white/20" /><span className="h-3 w-3 rounded-full bg-white/20" /><span className="h-3 w-3 rounded-full bg-pulse" /></div>
              <pre className="whitespace-pre-wrap">
                <span className="text-white">curl</span> -X POST {API_URL}/v1/payments \{"\n"}
                {"  "}-H <span className="text-white">&quot;Authorization: Bearer fc_test_…&quot;</span> \{"\n"}
                {"  "}-H <span className="text-pulse">&quot;Idempotency-Key: order-42&quot;</span> \{"\n"}
                {"  "}-d &apos;{"{"}&quot;amount&quot;:100000,&quot;currency&quot;:&quot;INR&quot;{"}"}&apos;{"\n\n"}
                <span className="text-white">HTTP/1.1 201 Created</span>{"\n"}
                <span className="text-pulse">Idempotent-Replayed: true</span>{"\n"}
                {"{"}{"\n"}
                {"  "}&quot;status&quot;: <span className="text-white">&quot;captured&quot;</span>,{"\n"}
                {"  "}&quot;amount_captured&quot;: 100000,{"\n"}
                {"  "}&quot;fee_amount&quot;: 2000{"\n"}
                {"}"}
              </pre>
            </Fade>
          </Wrap>
        </section>

        {/* Numbers */}
        <section id="numbers" className="scroll-mt-20 border-t border-mist py-24 md:py-32">
          <Wrap>
            <Title text="Numbers that" strong="add up." className="mx-auto mb-16 text-center text-heading md:text-heading-lg" />
            <div className="grid gap-x-10 sm:grid-cols-2">
              {[
                { v: 14825, label: "requests in a 30-second load test", note: "payments, retries and racing payouts at once" },
                { v: 0, label: "duplicate charges", note: "across ~2,000 triple-retried requests" },
                { v: 173, suffix: "ms", label: "p95 latency under load", note: "with every write row-locked and balanced" },
                { v: 86, label: "automated tests, race detector on", note: "against real Postgres and Dragonfly" },
              ].map((s) => (
                <div key={s.label} className="border-t border-ink py-10">
                  <div className="text-[clamp(72px,11vw,160px)] leading-[0.8] tracking-display"><Counter value={s.v} suffix={s.suffix} /></div>
                  <p className="mt-6 text-subheading">{s.label}</p>
                  <p className="mt-1 text-label text-warm-gray">{s.note}</p>
                </div>
              ))}
            </div>
          </Wrap>
        </section>

        {/* CTA banner */}
        <section className="pb-24">
          <Wrap>
            <div className="band relative overflow-hidden rounded-large px-8 py-16 md:px-16 md:py-20">
              <div className="halo" style={{ left: "80%", top: "50%", width: "55%" }} />
              <div className="pointer-events-none absolute right-10 top-10 hidden flex-col items-end gap-4 md:flex">
                <span className="float rounded-tag bg-white px-4 py-2 text-label text-ink">₹1,000.00 captured</span>
                <span className="float rounded-tag border border-pulse bg-ink px-4 py-2 text-label [animation-delay:-1.5s]">Debits = credits</span>
                <span className="float rounded-tag bg-white px-4 py-2 text-label text-ink [animation-delay:-3s]">evt_1042 delivered</span>
                <Sparkle className="wiggle text-pulse" size={40} />
              </div>
              <div className="relative max-w-xl">
                <div className="text-caption uppercase text-fog">Start in sandbox</div>
                <Title dark as="h2" text="Turn every payment into a" strong="balanced" after="entry." className="mt-4 text-heading md:text-heading-lg" />
                <div className="mt-10 flex flex-wrap gap-3">
                  <a href={DASHBOARD_URL} className="rounded-pill bg-white px-6 py-4 text-body text-ink transition-opacity hover:opacity-80">Open the dashboard</a>
                  <a href={`${API_URL}/openapi.yaml`} className="rounded-pill border border-white/40 px-6 py-4 text-body transition-colors hover:border-white">Read the API →</a>
                </div>
              </div>
            </div>
          </Wrap>
        </section>
      </main>
      <Footer />
      <Motion />
    </>
  );
}
