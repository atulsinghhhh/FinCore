import { Suspense } from "react";
import { api, type OutboxEvent, type Page } from "@/lib/api";
import { Pager } from "@/components/pager";
import { dateTime } from "@/lib/format";
import { Badge, Card, IdLink, PageHeader, Skeleton, Table, Tabs, Td, smallButtonClass } from "@/components/ui";
import { requeueOutboxEvent } from "@/app/actions";
import { requireSession } from "@/lib/session";

export const metadata = { title: "Webhooks" };

const statuses = ["", "pending", "delivered", "skipped", "dead"];

export default function OutboxPage({ searchParams }: PageProps<"/outbox">) {
  return (
    <>
      <PageHeader
        title="Webhooks"
        description="Events written to the transactional outbox in the same transaction as each state change, and their signed delivery to merchant endpoints."
      />
      <Suspense fallback={<Skeleton rows={12} />}>
        <Events searchParams={searchParams} />
      </Suspense>
    </>
  );
}

async function Events({ searchParams }: { searchParams: PageProps<"/outbox">["searchParams"] }) {
  const sp = await searchParams;
  const isAdmin = (await requireSession()).role === "admin";
  const status = typeof sp.status === "string" ? sp.status : "";
  const cursor = typeof sp.cursor === "string" ? sp.cursor : "";
  const q = new URLSearchParams({ limit: "50", ...(status && { status }), ...(cursor && { cursor }) });
  const { data, next_cursor } = await api<Page<OutboxEvent>>(`/v1/admin/outbox?${q}`);
  return (
    <>
      <Card flush>
        <div className="px-6 pt-5">
      <Tabs active={status} items={statuses.map((s) => ({ value: s, label: s || "All", href: s ? `/outbox?status=${s}` : "/outbox" }))} />
        </div>
        <div className="px-6 pb-6">
      <Table head={["Event", "Type", "Merchant", "Status", "Attempts", "Last error", "Created", ""]} empty={data.length === 0}>
        {data.map((e) => (
          <tr key={e.id}>
            <Td mono>evt_{e.id}</Td>
            <Td mono>{e.event_type}</Td>
            <Td><IdLink href={`/merchants/${e.merchant_id}`} id={e.merchant_id} /></Td>
            <Td><Badge>{e.status}</Badge></Td>
            <Td numeric>{e.attempts}</Td>
            <Td className="max-w-xs truncate" muted>{e.last_error ?? ""}</Td>
            <Td muted>{dateTime(e.created_at)}</Td>
            <Td>
              {isAdmin && e.status === "dead" && (
                <form action={requeueOutboxEvent}>
                  <input type="hidden" name="id" value={e.id} />
                  <button className={smallButtonClass}>Retry</button>
                </form>
              )}
            </Td>
          </tr>
        ))}
      </Table>
      <Pager href="/outbox" params={status ? { status } : {}} cursor={cursor} nextCursor={next_cursor} shown={data.length} />
        </div>
      </Card>
    </>
  );
}
