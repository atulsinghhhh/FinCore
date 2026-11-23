import { Suspense } from "react";
import { api, type Account, type Integrity } from "@/lib/api";
import { money } from "@/lib/format";
import { Card, Badge, PageHeader, Skeleton, Table, Td } from "@/components/ui";

export const metadata = { title: "Ledger" };

export default function LedgerPage() {
  return (
    <>
      <PageHeader
        title="Ledger"
        description="Double-entry accounts. Balances are derived from immutable postings and re-verified every time this page loads."
      />
      <Suspense fallback={<Skeleton rows={12} />}>
        <Ledger />
      </Suspense>
    </>
  );
}

async function Ledger() {
  const [integrity, accounts] = await Promise.all([api<Integrity>("/v1/admin/ledger/integrity"), api<{ data: Account[] }>("/v1/admin/ledger/accounts")]);
  return (
    <>
      <div className={`mb-16 flex flex-wrap items-center gap-4 rounded-card border p-6 ${integrity.ok ? "border-ink" : "border-pulse"}`}>
        <Badge>{integrity.ok ? "balanced" : "unbalanced"}</Badge>
        <p className="text-body">
          {integrity.ok
            ? "Integrity check passed — every currency balances and every cached balance matches its postings."
            : `Integrity check failed — ${integrity.mismatches?.length ?? 0} cached balance(s) differ from postings or a currency is unbalanced.`}
        </p>
      </div>
      <Card title={`Accounts · ${accounts.data.length}`}>
      <Table head={["Code", "Name", "Type", "Debits", "Credits", "Balance"]} empty={accounts.data.length === 0}>
        {accounts.data.map((a) => (
          <tr key={a.id}>
            <Td mono>{a.code}</Td>
            <Td muted>{a.name}</Td>
            <Td muted>{a.type}</Td>
            <Td numeric muted>{money(a.debits_total, a.currency)}</Td>
            <Td numeric muted>{money(a.credits_total, a.currency)}</Td>
            <Td numeric>{money(a.balance, a.currency)}</Td>
          </tr>
        ))}
      </Table>
      </Card>
    </>
  );
}
