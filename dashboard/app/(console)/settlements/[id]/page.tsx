import { Suspense } from "react";
import { api, type SettlementReport } from "@/lib/api";
import { dateTime, money } from "@/lib/format";
import { Badge, Card, IdLink, PageHeader, Skeleton, Stat, StatHero, StatRow, Table, Td, TextLink } from "@/components/ui";

export const metadata = { title: "Reconciliation run" };

export default function SettlementPage({ params }: PageProps<"/settlements/[id]">) {
  return (
    <Suspense fallback={<div className="pt-16"><Skeleton rows={14} /></div>}>
      <Report params={params} />
    </Suspense>
  );
}

async function Report({ params }: { params: PageProps<"/settlements/[id]">["params"] }) {
  const { id } = await params;
  const r = await api<SettlementReport>(`/v1/admin/settlements/${id}`);
  const s = r.summary;
  const lines = r.lines ?? [];
  const exceptions = lines.filter((l) => l.result !== "matched");

  return (
    <>
      <PageHeader title={r.source} description={`${r.provider} settlement · imported ${dateTime(r.created_at)}`} />
      <StatHero label="Net to bank" value={money(s.net_to_bank)} note={`${money(s.gross_settled)} gross · ${money(s.refunded)} refunds`} />
      <StatRow>
        <Stat label="Matched" value={`${s.matched}/${s.lines}`} />
        <Stat label="Exceptions" value={s.exceptions} alert={s.exceptions > 0} />
        <Stat label="Unsettled" value={s.unsettled?.length ?? 0} alert={(s.unsettled?.length ?? 0) > 0} hint="captured > 3 days ago" />
        <Stat label="Processor fees" value={money(s.fees)} />
      </StatRow>

      <div className="space-y-6">
        {exceptions.length > 0 && (
          <Card title={`Exceptions · ${exceptions.length}`} className="border-pulse">
            <Table head={["Line", "Type", "Provider ID", "Result", "Detail", "Amount"]}>
              {exceptions.map((l) => (
                <tr key={l.line_no}>
                  <Td numeric muted>{l.line_no}</Td>
                  <Td muted>{l.entity_type}</Td>
                  <Td mono>{l.entity_id}</Td>
                  <Td><Badge>{l.result}</Badge></Td>
                  <Td className="max-w-sm whitespace-normal text-warm-gray">{l.detail}</Td>
                  <Td numeric>{money(l.amount, l.currency)}</Td>
                </tr>
              ))}
            </Table>
          </Card>
        )}

        {s.unsettled?.length > 0 && (
          <Card title={`Captured but not settled after 3 days · ${s.unsettled.length}`}>
            <Table head={["Payment", "Provider ID", "Amount", "Captured"]}>
              {s.unsettled.map((u) => (
                <tr key={u.payment_id}>
                  <Td><IdLink href={`/payments/${u.payment_id}`} id={u.payment_id} /></Td>
                  <Td mono>{u.provider_payment_id}</Td>
                  <Td numeric>{money(u.amount)}</Td>
                  <Td muted>{dateTime(u.captured_at)}</Td>
                </tr>
              ))}
            </Table>
          </Card>
        )}

        <Card title={`All lines · ${lines.length}`}>
          <Table head={["Line", "Settlement", "Type", "Provider ID", "Result", "Amount", "Fee", "Tax", "Settled"]}>
            {lines.map((l) => (
              <tr key={l.line_no}>
                <Td numeric muted>{l.line_no}</Td>
                <Td mono>{l.settlement_id}</Td>
                <Td muted>{l.entity_type}</Td>
                <Td>{l.payment_id ? <TextLink href={`/payments/${l.payment_id}`} className="font-mono text-[13px]">{l.entity_id}</TextLink> : <span className="font-mono text-[13px]">{l.entity_id}</span>}</Td>
                <Td><Badge>{l.result}</Badge></Td>
                <Td numeric>{money(l.amount, l.currency)}</Td>
                <Td numeric muted>{money(l.fee, l.currency)}</Td>
                <Td numeric muted>{money(l.tax, l.currency)}</Td>
                <Td muted>{dateTime(l.settled_at)}</Td>
              </tr>
            ))}
          </Table>
        </Card>
      </div>
      <p className="mt-12"><TextLink href="/settlements" className="text-data">← All runs</TextLink></p>
    </>
  );
}
