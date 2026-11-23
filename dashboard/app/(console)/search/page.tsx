import { Suspense } from "react";
import { api, type Merchant, type Payment } from "@/lib/api";
import { dateTime, money } from "@/lib/format";
import { Badge, Card, IdLink, PageHeader, Skeleton, Table, Td, TextLink } from "@/components/ui";

export const metadata = { title: "Search" };

export default function SearchPage({ searchParams }: PageProps<"/search">) {
  return (
    <Suspense fallback={<><PageHeader title="Search" /><Skeleton rows={8} /></>}>
      <Results searchParams={searchParams} />
    </Suspense>
  );
}

async function Results({ searchParams }: { searchParams: PageProps<"/search">["searchParams"] }) {
  const sp = await searchParams;
  const q = typeof sp.q === "string" ? sp.q.trim() : "";
  const res = q.length >= 2 ? await api<{ payments: Payment[]; merchants: Merchant[] }>(`/v1/admin/search?q=${encodeURIComponent(q)}`) : { payments: [], merchants: [] };
  const none = res.payments.length + res.merchants.length === 0;
  return (
    <>
      <PageHeader title={q ? `Results for “${q}”` : "Search"} description="Payments by ID, provider order or payment ID, or description · merchants by ID or name. Press ⌘K anywhere." />
      {q.length < 2 && <p className="text-data text-warm-gray">Type at least two characters in the search box above.</p>}
      {q.length >= 2 && none && <p className="text-data text-warm-gray">Nothing matches. Try a payment ID prefix such as the first 8 characters, a Razorpay order_ or pay_ ID, or a merchant name.</p>}
      <div className="space-y-4">
        {res.merchants.length > 0 && (
          <Card title={`Merchants · ${res.merchants.length}`}>
            <Table head={["Merchant", "Name", "Fee", "Created"]}>
              {res.merchants.map((m) => (
                <tr key={m.id}>
                  <Td><IdLink href={`/merchants/${m.id}`} id={m.id} /></Td>
                  <Td><TextLink href={`/merchants/${m.id}`}>{m.name}</TextLink></Td>
                  <Td muted>{(m.fee_bps / 100).toFixed(2)}%</Td>
                  <Td muted>{dateTime(m.created_at)}</Td>
                </tr>
              ))}
            </Table>
          </Card>
        )}
        {res.payments.length > 0 && (
          <Card title={`Payments · ${res.payments.length}`}>
            <Table head={["Payment", "Description", "Status", "Amount", "Created"]}>
              {res.payments.map((p) => (
                <tr key={p.id}>
                  <Td><IdLink href={`/payments/${p.id}`} id={p.id} /></Td>
                  <Td>{p.description || <span className="text-fog">—</span>}</Td>
                  <Td><Badge>{p.status}</Badge></Td>
                  <Td numeric>{money(p.amount, p.currency)}</Td>
                  <Td muted>{dateTime(p.created_at)}</Td>
                </tr>
              ))}
            </Table>
          </Card>
        )}
      </div>
    </>
  );
}
