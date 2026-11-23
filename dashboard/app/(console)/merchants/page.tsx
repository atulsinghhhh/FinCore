import { Suspense } from "react";
import { api, type Merchant, type MerchantBalance } from "@/lib/api";
import { dateTime, money } from "@/lib/format";
import { Card, IdLink, PageHeader, Skeleton, Table, Td } from "@/components/ui";
import { CreateMerchantForm } from "@/components/create-merchant-form";
import { requireSession } from "@/lib/session";

export const metadata = { title: "Merchants" };

export default function MerchantsPage() {
  return (
    <>
      <PageHeader title="Merchants" description="Each merchant has a payable account in the ledger, API keys, and an optional webhook endpoint." />
      <Suspense fallback={null}>
        <AdminOnly>
          <Card title="New merchant" className="mb-16">
            <CreateMerchantForm />
          </Card>
        </AdminOnly>
      </Suspense>
      <Suspense fallback={<Skeleton rows={6} />}>
        <MerchantList />
      </Suspense>
    </>
  );
}

async function AdminOnly({ children }: { children: React.ReactNode }) {
  return (await requireSession()).role === "admin" ? children : null;
}

async function MerchantList() {
  const { data } = await api<{ data: Merchant[] }>("/v1/admin/merchants");
  const balances = await Promise.all(data.map((m) => api<MerchantBalance>(`/v1/admin/merchants/${m.id}/balance`)));
  return (
    <>
      <Card title={`All merchants · ${data.length}`}>
      <Table head={["Merchant", "Name", "Fee", "Balance", "Pending payouts", "Webhooks", "Created"]} empty={data.length === 0}>
        {data.map((m, i) => (
          <tr key={m.id}>
            <Td><IdLink href={`/merchants/${m.id}`} id={m.id} /></Td>
            <Td>{m.name}</Td>
            <Td numeric muted>{(m.fee_bps / 100).toFixed(2)}%</Td>
            <Td numeric>{money(balances[i].available, m.currency)}</Td>
            <Td numeric muted>{money(balances[i].pending_payouts, m.currency)}</Td>
            <Td muted>{m.webhook_url ? "Configured" : "—"}</Td>
            <Td muted>{dateTime(m.created_at)}</Td>
          </tr>
        ))}
      </Table>
      </Card>
    </>
  );
}
