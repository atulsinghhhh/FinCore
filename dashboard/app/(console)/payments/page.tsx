import { Suspense } from "react";
import { api, type Page, type Payment } from "@/lib/api";
import { Pager } from "@/components/pager";
import { dateTime, money } from "@/lib/format";
import { Badge, Card, IdLink, PageHeader, Skeleton, Table, Tabs, Td } from "@/components/ui";

export const metadata = { title: "Payments" };

const statuses = ["", "created", "authorized", "captured", "partially_refunded", "refunded", "failed", "canceled"];

export default function PaymentsPage({ searchParams }: PageProps<"/payments">) {
  return (
    <>
      <PageHeader title="Payments" description="All merchants, newest first. Open a payment to see its audit trail and the ledger entries behind it." />
      <Suspense fallback={<Skeleton rows={12} />}>
        <PaymentList searchParams={searchParams} />
      </Suspense>
    </>
  );
}

async function PaymentList({ searchParams }: { searchParams: PageProps<"/payments">["searchParams"] }) {
  const sp = await searchParams;
  const status = typeof sp.status === "string" ? sp.status : "";
  const merchant = typeof sp.merchant_id === "string" ? sp.merchant_id : "";
  const cursor = typeof sp.cursor === "string" ? sp.cursor : "";
  const filters = { ...(status && { status }), ...(merchant && { merchant_id: merchant }) };
  const q = new URLSearchParams({ limit: "50", ...filters, ...(cursor && { cursor }) });
  const { data, next_cursor } = await api<Page<Payment>>(`/v1/admin/payments?${q}`);
  const withMerchant = (s: string) => {
    const p = new URLSearchParams({ ...(s && { status: s }), ...(merchant && { merchant_id: merchant }) });
    return `/payments${p.size ? `?${p}` : ""}`;
  };

  return (
    <>
      <Card flush>
        <div className="px-6 pt-5">
      <Tabs active={status} items={statuses.map((s) => ({ value: s, label: s ? s.replaceAll("_", " ") : "All", href: withMerchant(s) }))} />
        </div>
        <div className="px-6 pb-6">
      <Table head={["Payment", "Description", "Merchant", "Status", "Provider", "Amount", "Refunded", "Fee", "Created"]} empty={data.length === 0}>
        {data.map((p) => (
          <tr key={p.id}>
            <Td><IdLink href={`/payments/${p.id}`} id={p.id} /></Td>
            <Td className="max-w-56 truncate">{p.description || <span className="text-fog">—</span>}</Td>
            <Td><IdLink href={`/merchants/${p.merchant_id}`} id={p.merchant_id} /></Td>
            <Td><Badge>{p.status}</Badge></Td>
            <Td muted>{p.provider}</Td>
            <Td numeric>{money(p.amount, p.currency)}</Td>
            <Td numeric muted>{p.amount_refunded ? money(p.amount_refunded, p.currency) : "—"}</Td>
            <Td numeric muted>{money(p.fee_amount, p.currency)}</Td>
            <Td muted>{dateTime(p.created_at)}</Td>
          </tr>
        ))}
      </Table>
      <Pager href="/payments" params={filters} cursor={cursor} nextCursor={next_cursor} shown={data.length} />
        </div>
      </Card>
    </>
  );
}
