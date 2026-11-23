import { Suspense } from "react";
import Link from "next/link";
import { AlertTriangle, ArrowDownRight, ArrowUpRight, CheckCircle2 } from "lucide-react";
import { api, type Integrity, type Page, type Payment, type Stats } from "@/lib/api";
import { count, dateTime, delta, money, moneyCompact, percent } from "@/lib/format";
import { AreaTrend, RankedBars, Sparkline, StackedColumns } from "@/components/charts";
import { Badge, Card, IdLink, PageHeader, Skeleton, Table, Td, TextLink } from "@/components/ui";

type Totals = { captured_volume: number; succeeded: number; failed: number; total: number; refunded_volume: number; fees: number; success_rate: number; avg_ticket: number };
type Analytics = {
  days: number;
  from: string;
  to: string;
  series: { day: string; captured_volume: number; succeeded: number; failed: number; total: number; refunded_volume: number; fees: number }[];
  current: Totals;
  previous: Totals;
  statuses: { status: string; count: number; amount: number }[];
  top_merchants: { id: string; name: string; captured_volume: number; succeeded: number }[];
};

const ranges = [7, 30, 90];

export default function OverviewPage({ searchParams }: PageProps<"/">) {
  return (
    <Suspense fallback={<><PageHeader title="Overview" /><Skeleton rows={14} /></>}>
      <Overview searchParams={searchParams} />
    </Suspense>
  );
}

async function Overview({ searchParams }: { searchParams: PageProps<"/">["searchParams"] }) {
  const sp = await searchParams;
  const days = ranges.includes(Number(sp.range)) ? Number(sp.range) : 30;
  const [a, stats, integrity, recent] = await Promise.all([
    api<Analytics>(`/v1/admin/analytics?days=${days}`),
    api<Stats>("/v1/admin/stats"),
    api<Integrity>("/v1/admin/ledger/integrity"),
    api<Page<Payment>>("/v1/admin/payments?limit=6"),
  ]);
  const c = a.current;
  const p = a.previous;
  const range = (
    <nav className="inline-flex rounded-lg border border-mist p-0.5" aria-label="Date range">
      {ranges.map((r) => (
        <Link key={r} href={r === 30 ? "/" : `/?range=${r}`} aria-current={r === days ? "page" : undefined}
          className={`rounded-md px-3 py-1 text-data transition-colors ${r === days ? "bg-ink text-white" : "text-slate hover:text-ink"}`}>
          {r}d
        </Link>
      ))}
    </nav>
  );

  return (
    <>
      <PageHeader title="Overview" description={`${fmtDay(a.from)} – ${fmtDay(a.to)} · compared with the previous ${days} days · IST`} actions={range} />

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-5">
        <Kpi label="Captured volume" value={moneyCompact(c.captured_volume)} title={money(c.captured_volume)} change={delta(c.captured_volume, p.captured_volume)} spark={a.series.map((d) => d.captured_volume)} />
        <Kpi label="Successful payments" value={count(c.succeeded)} change={delta(c.succeeded, p.succeeded)} spark={a.series.map((d) => d.succeeded)} />
        <Kpi label="Success rate" value={percent(c.success_rate)} change={p.success_rate ? c.success_rate - p.success_rate : null} points spark={a.series.map((d) => (d.succeeded + d.failed ? d.succeeded / (d.succeeded + d.failed) : 1))} />
        <Kpi label="Average ticket" value={money(c.avg_ticket)} change={delta(c.avg_ticket, p.avg_ticket)} spark={a.series.map((d) => (d.succeeded ? d.captured_volume / d.succeeded : 0))} />
        <Kpi label="Platform fees" value={moneyCompact(c.fees)} title={money(c.fees)} change={delta(c.fees, p.fees)} spark={a.series.map((d) => d.fees)} />
      </div>

      <div className="mt-4 grid gap-4 xl:grid-cols-3">
        <Card title="Captured volume" className="xl:col-span-2" actions={<span className="text-data text-warm-gray">per day · {money(c.captured_volume)} total</span>}>
          <AreaTrend unit="money" label="Captured volume" data={a.series.map((d) => ({ day: d.day, value: d.captured_volume }))} />
        </Card>
        <Card title="Payments by status" actions={<span className="text-data text-warm-gray">{count(c.total)} total</span>}>
          <RankedBars unit="count" items={a.statuses.map((s) => ({ label: sentence(s.status), value: s.count, href: `/payments?status=${s.status}`, note: percentOf(s.count, c.total) }))} />
        </Card>
      </div>

      <div className="mt-4 grid gap-4 xl:grid-cols-3">
        <Card title="Successful vs failed payments" className="xl:col-span-2" actions={<span className="text-data text-warm-gray">{count(c.failed)} failed · {percent(1 - c.success_rate)} failure rate</span>}>
          <StackedColumns series={["Successful", "Failed"]} data={a.series.map((d) => ({ day: d.day, a: d.succeeded, b: d.failed }))} />
        </Card>
        <Card title="Top merchants" actions={<TextLink href="/merchants" className="text-data">All merchants</TextLink>}>
          <RankedBars unit="money" items={a.top_merchants.map((m) => ({ label: m.name, value: m.captured_volume, href: `/merchants/${m.id}`, note: `${count(m.succeeded)} paid` }))} />
        </Card>
      </div>

      <h2 className="mt-10 mb-4 text-data">System health</h2>
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <Health ok={integrity.ok} label="Ledger integrity" detail={integrity.ok ? "Debits = credits; balances match postings" : "Balances disagree with postings"} href="/ledger" />
        <Health ok={stats.dead_outbox_events === 0} label="Webhook delivery" detail={stats.dead_outbox_events ? `${stats.dead_outbox_events} dead-lettered` : "No dead letters"} href="/outbox?status=dead" />
        <Health ok={stats.unprocessed_webhooks === 0} label="Provider webhooks" detail={stats.unprocessed_webhooks ? `${stats.unprocessed_webhooks} awaiting retry` : "All processed"} href="/outbox" />
        <Health ok={stats.reconciliation_exceptions === 0} label="Reconciliation" detail={stats.reconciliation_exceptions ? `${stats.reconciliation_exceptions} exceptions to review` : "No exceptions"} href="/settlements" />
      </div>

      <Card title="Recent payments" className="mt-4" actions={<TextLink href="/payments" className="text-data">View all</TextLink>}>
        <Table head={["Payment", "Description", "Status", "Amount", "Created"]} empty={recent.data.length === 0}>
          {recent.data.map((r) => (
            <tr key={r.id}>
              <Td><IdLink href={`/payments/${r.id}`} id={r.id} /></Td>
              <Td className="max-w-64 truncate">{r.description || <span className="text-fog">—</span>}</Td>
              <Td><Badge>{r.status}</Badge></Td>
              <Td numeric>{money(r.amount, r.currency)}</Td>
              <Td muted>{dateTime(r.created_at)}</Td>
            </tr>
          ))}
        </Table>
      </Card>
    </>
  );
}

function Kpi({ label, value, title, change, spark, points }: { label: string; value: string; title?: string; change: number | null; spark: number[]; points?: boolean }) {
  const up = (change ?? 0) >= 0;
  return (
    <div className="rounded-card border border-mist bg-white p-5">
      <div className="text-data text-warm-gray">{label}</div>
      <div className="mt-2 text-heading tabular-nums" title={title}>{value}</div>
      <div className="mt-3 flex items-end justify-between gap-2">
        {change === null ? (
          <span className="text-data text-fog">No prior data</span>
        ) : (
          <span className={`inline-flex items-center gap-1 text-data tabular-nums ${up ? "" : "text-ink"}`} title="vs previous period">
            {!up && <span className="h-1.5 w-1.5 rounded-full bg-pulse" aria-hidden />}
            {up ? <ArrowUpRight size={14} aria-hidden /> : <ArrowDownRight size={14} aria-hidden />}
            {points ? `${up ? "+" : ""}${(change * 100).toFixed(1)} pts` : `${up ? "+" : ""}${(change * 100).toFixed(1)}%`}
          </span>
        )}
        <Sparkline values={spark} width={96} height={28} />
      </div>
    </div>
  );
}

function Health({ ok, label, detail, href }: { ok: boolean; label: string; detail: string; href: string }) {
  return (
    <Link href={href} className={`flex items-start gap-3 rounded-card border p-4 transition-colors hover:border-ink ${ok ? "border-mist" : "border-pulse"} bg-white`}>
      {ok ? <CheckCircle2 size={18} strokeWidth={1.5} className="mt-0.5 shrink-0" aria-hidden /> : <AlertTriangle size={18} strokeWidth={1.5} className="mt-0.5 shrink-0 text-pulse" aria-hidden />}
      <span>
        <span className="block text-data">{label} <span className="sr-only">{ok ? "healthy" : "needs attention"}</span></span>
        <span className="block text-data text-warm-gray">{detail}</span>
      </span>
    </Link>
  );
}

function sentence(status: string) {
  const s = status.replaceAll("_", " ");
  return s.charAt(0).toUpperCase() + s.slice(1);
}

function percentOf(n: number, total: number) {
  return total ? `${((n / total) * 100).toFixed(1)}%` : "";
}

function fmtDay(iso: string) {
  return new Intl.DateTimeFormat("en-IN", { day: "numeric", month: "short", year: "numeric" }).format(new Date(iso + "T00:00:00+05:30"));
}
