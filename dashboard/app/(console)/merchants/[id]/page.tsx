import { Suspense } from "react";
import { api, type APIKey, type Merchant, type MerchantBalance, type Page, type Payout, type Statement } from "@/lib/api";
import { currentMode } from "@/lib/mode";
import { requireSession } from "@/lib/session";
import { Pager } from "@/components/pager";
import { dateTime, money } from "@/lib/format";
import { Badge, Card, PageHeader, Skeleton, Stat, StatHero, StatRow, Table, Td, TextLink, smallButtonClass } from "@/components/ui";
import { settlePayout } from "@/app/actions";
import { IssueKey, RevokeKey } from "./api-keys";

export const metadata = { title: "Merchant" };

export default function MerchantPage({ params, searchParams }: PageProps<"/merchants/[id]">) {
  return (
    <Suspense fallback={<div className="pt-16"><Skeleton rows={14} /></div>}>
      <Detail params={params} searchParams={searchParams} />
    </Suspense>
  );
}

async function Detail({ params, searchParams }: { params: PageProps<"/merchants/[id]">["params"]; searchParams: PageProps<"/merchants/[id]">["searchParams"] }) {
  const { id } = await params;
  const sp = await searchParams;
  const cursor = typeof sp.cursor === "string" ? sp.cursor : "";
  const isAdmin = (await requireSession()).role === "admin";
  const sandbox = (await currentMode()) === "test" && isAdmin;
  const [m, balance, statement, payouts, keys] = await Promise.all([
    api<Merchant>(`/v1/admin/merchants/${id}`),
    api<MerchantBalance>(`/v1/admin/merchants/${id}/balance`),
    api<Statement>(`/v1/admin/merchants/${id}/statement`),
    api<Page<Payout>>(`/v1/admin/payouts?merchant_id=${id}&limit=10${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`),
    api<{ data: APIKey[] }>(`/v1/admin/merchants/${id}/api-keys`),
  ]);
  const lines = [...statement.lines].reverse();
  const activeKeys = keys.data.filter((k) => !k.revoked_at).length;

  return (
    <>
      <PageHeader
        title={m.name}
        description={`Merchant ${m.id}`}
        actions={<TextLink href={`/payments?merchant_id=${m.id}`} className="text-body">View payments →</TextLink>}
      />
      <StatHero label="Available to pay out" value={money(balance.available, m.currency)} />
      <StatRow>
        <Stat label="Pending payouts" value={money(balance.pending_payouts, m.currency)} />
        <Stat label="Platform fee" value={`${(m.fee_bps / 100).toFixed(2)}%`} />
        <Stat label="API keys" value={keys.data.filter((k) => !k.revoked_at).length} hint="active" />
        <Stat label="Webhooks" value={m.webhook_url ? "On" : "Off"} hint={m.webhook_url ?? "No endpoint configured"} />
      </StatRow>

      <Card title="Statement · last 30 days" actions={<span className="text-data text-warm-gray">Opening {money(statement.opening_balance, m.currency)} · Closing {money(statement.closing_balance, m.currency)}</span>}>
        <Table head={["Date", "Description", "Debits", "Credits", "Balance"]} empty={lines.length === 0}>
          {lines.map((l) => (
            <tr key={l.posting_id}>
              <Td muted>{dateTime(l.effective_at)}</Td>
              <Td className="max-w-md truncate">{l.description}</Td>
              <Td numeric>{l.direction === "debit" ? money(l.amount, m.currency) : ""}</Td>
              <Td numeric>{l.direction === "credit" ? money(l.amount, m.currency) : ""}</Td>
              <Td numeric>{money(l.balance, m.currency)}</Td>
            </tr>
          ))}
        </Table>
      </Card>

      <div className="mt-6 grid gap-6">
        <Card title="Payouts">
          <Table head={["Payout", "Status", "Amount", "Created", ""]} empty={payouts.data.length === 0}>
            {payouts.data.map((p) => (
              <tr key={p.id}>
                <Td mono>{p.id.slice(0, 8)}</Td>
                <Td><Badge>{p.status}</Badge></Td>
                <Td numeric>{money(p.amount, p.currency)}</Td>
                <Td muted>{dateTime(p.created_at)}</Td>
                <Td>
                  {sandbox && p.status === "pending" && (
                    <form action={settlePayout} className="flex gap-2">
                      <input type="hidden" name="id" value={p.id} />
                      <button name="outcome" value="paid" className={smallButtonClass}>Bank paid</button>
                      <button name="outcome" value="failed" className={smallButtonClass}>Rejected</button>
                    </form>
                  )}
                </Td>
              </tr>
            ))}
          </Table>
          <Pager href={`/merchants/${m.id}`} params={{}} cursor={cursor} nextCursor={payouts.next_cursor} shown={payouts.data.length} />
        </Card>
        <Card title={`API keys · ${activeKeys} active`}>
          {isAdmin && (
            <div className="mb-8 border-b border-mist pb-8">
              <IssueKey merchantId={m.id} />
            </div>
          )}
          <Table head={["Key", "Created", "Last used", "Status", ""]} empty={keys.data.length === 0}>
            {keys.data.map((k) => (
              <tr key={k.id}>
                <Td mono>{k.prefix}…</Td>
                <Td muted>{dateTime(k.created_at)}</Td>
                <Td muted>{k.last_used_at ? dateTime(k.last_used_at) : "never"}</Td>
                <Td>
                  <Badge>{k.revoked_at ? "revoked" : "active"}</Badge>
                  {k.revoked_at && <span className="ml-2 text-data text-warm-gray">{dateTime(k.revoked_at)}</span>}
                </Td>
                <Td className="text-right">
                  {isAdmin && !k.revoked_at && <RevokeKey merchantId={m.id} keyId={k.id} prefix={k.prefix} lastActive={activeKeys === 1} />}
                </Td>
              </tr>
            ))}
          </Table>
          {!isAdmin && <p className="mt-6 text-data text-warm-gray">Only admins can issue or revoke keys.</p>}
        </Card>
      </div>
    </>
  );
}
