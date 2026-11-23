import { Suspense } from "react";
import { api, type Account, type LedgerTransaction, type PaymentDetail } from "@/lib/api";
import { dateTime, money } from "@/lib/format";
import { Badge, Card, Field, IdLink, PageHeader, Skeleton, StatHero, Table, Td, TextLink } from "@/components/ui";

export const metadata = { title: "Payment" };

export default function PaymentPage({ params }: PageProps<"/payments/[id]">) {
  return (
    <Suspense fallback={<div className="pt-16"><Skeleton rows={14} /></div>}>
      <Detail params={params} />
    </Suspense>
  );
}

async function Detail({ params }: { params: PageProps<"/payments/[id]">["params"] }) {
  const { id } = await params;
  const [p, accounts] = await Promise.all([api<PaymentDetail>(`/v1/admin/payments/${id}`), api<{ data: Account[] }>("/v1/admin/ledger/accounts")]);
  const txnIds = [p.capture_ledger_txn_id, ...p.refunds.flatMap((r) => [r.ledger_txn_id, r.reversal_ledger_txn_id])].filter((x): x is string => Boolean(x));
  const txns = await Promise.all(txnIds.map((t) => api<LedgerTransaction>(`/v1/admin/ledger/transactions/${t}`)));
  const code = new Map(accounts.data.map((a) => [a.id, a.code]));

  return (
    <>
      <PageHeader
        title={p.description || "Payment"}
        description={`${p.provider} · ${p.capture_method} capture · created ${dateTime(p.created_at)}`}
        actions={<Badge>{p.status}</Badge>}
      />
      <StatHero label="Amount" value={money(p.amount, p.currency)} note={p.id} />

      <dl className="grid grid-cols-2 gap-x-6 gap-y-8 py-12 md:grid-cols-4">
        <Field label="Captured">{money(p.amount_captured, p.currency)}</Field>
        <Field label="Refunded">{money(p.amount_refunded, p.currency)}</Field>
        <Field label="Platform fee">{money(p.fee_amount, p.currency)}</Field>
        <Field label="Merchant"><IdLink href={`/merchants/${p.merchant_id}`} id={p.merchant_id} /></Field>
        <Field label="Provider order"><span className="break-all font-mono text-[13px]">{p.provider_ref ?? "—"}</span></Field>
        <Field label="Provider payment"><span className="break-all font-mono text-[13px]">{p.provider_payment_id ?? "—"}</span></Field>
        <Field label="Failure">{p.failure_reason ?? "—"}</Field>
        <Field label="Updated">{dateTime(p.updated_at)}</Field>
      </dl>

      <div className="grid gap-6 lg:grid-cols-2">
        <Card title="Audit trail">
          <ol className="relative space-y-6 border-l border-mist pl-6">
            {p.events.map((e, i) => (
              <li key={e.id} className="relative">
                <span className={`absolute -left-[29px] top-1.5 h-2.5 w-2.5 rounded-full ${i === p.events.length - 1 ? "bg-pulse" : "bg-ink"}`} aria-hidden />
                <div className="flex flex-wrap items-center gap-2">
                  {e.from_status && <Badge>{e.from_status}</Badge>}
                  {e.from_status && <span className="text-fog">→</span>}
                  <Badge>{e.to_status}</Badge>
                  {e.amount != null && <span className="text-data tabular-nums">{money(e.amount, p.currency)}</span>}
                </div>
                <p className="mt-1.5 text-data text-warm-gray">{e.note} · {dateTime(e.created_at)}</p>
              </li>
            ))}
          </ol>
        </Card>
        <Card title="Refunds">
          <Table head={["Refund", "Status", "Amount", "Reason"]} empty={p.refunds.length === 0}>
            {p.refunds.map((r) => (
              <tr key={r.id}>
                <Td mono>{r.id.slice(0, 8)}</Td>
                <Td><Badge>{r.status}</Badge></Td>
                <Td numeric>{money(r.amount, r.currency)}</Td>
                <Td muted>{r.reason || "—"}</Td>
              </tr>
            ))}
          </Table>
        </Card>
      </div>

      <h2 className="mt-20 mb-2 text-heading">Ledger entries</h2>
      <p className="mb-8 text-body text-warm-gray">Balanced, immutable transactions recording this payment&apos;s money movement.</p>
      {txns.length === 0 && <p className="text-body text-warm-gray">No money has moved for this payment.</p>}
      <div className="space-y-6">
        {txns.map((t) => (
          <Card key={t.id} title={t.description} actions={<span className="font-mono text-[13px] text-warm-gray">{t.id.slice(0, 8)} · {dateTime(t.effective_at)}</span>}>
            <Table head={["Account", "Debits", "Credits"]}>
              {t.postings.map((x) => (
                <tr key={x.id}>
                  <Td mono>{code.get(x.account_id) ?? x.account_id}</Td>
                  <Td numeric>{x.direction === "debit" ? money(x.amount, x.currency) : ""}</Td>
                  <Td numeric>{x.direction === "credit" ? money(x.amount, x.currency) : ""}</Td>
                </tr>
              ))}
            </Table>
          </Card>
        ))}
      </div>
      <p className="mt-12"><TextLink href="/payments" className="text-data">← All payments</TextLink></p>
    </>
  );
}
