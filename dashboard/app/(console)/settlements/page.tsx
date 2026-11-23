import { Suspense } from "react";
import { api, type SettlementReport } from "@/lib/api";
import { dateTime, money } from "@/lib/format";
import { Card, PageHeader, Skeleton, Table, Td, TextLink } from "@/components/ui";
import { SettlementUpload } from "@/components/settlement-upload";
import { requireSession } from "@/lib/session";

export const metadata = { title: "Reconciliation" };

export default function SettlementsPage() {
  return (
    <>
      <PageHeader
        title="Reconciliation"
        description="Match the processor's settlement report against the ledger. Matched lines move money from clearing to the bank; everything else becomes an exception."
      />
      <Suspense fallback={null}>
        <AdminOnly>
          <Card title="Import settlement file" className="mb-16">
            <SettlementUpload />
          </Card>
        </AdminOnly>
      </Suspense>
      <Suspense fallback={<Skeleton rows={6} />}>
        <Runs />
      </Suspense>
    </>
  );
}

async function AdminOnly({ children }: { children: React.ReactNode }) {
  return (await requireSession()).role === "admin" ? children : null;
}

async function Runs() {
  const { data } = await api<{ data: SettlementReport[] }>("/v1/admin/settlements");
  return (
    <>
      <Card title={`Runs · ${data.length}`}>
      <Table head={["File", "Provider", "Lines", "Matched", "Exceptions", "Unsettled", "Net to bank", "Imported"]} empty={data.length === 0}>
        {data.map((r) => (
          <tr key={r.id}>
            <Td><TextLink href={`/settlements/${r.id}`}>{r.source}</TextLink></Td>
            <Td muted>{r.provider}</Td>
            <Td numeric>{r.summary.lines}</Td>
            <Td numeric>{r.summary.matched}</Td>
            <Td numeric>
              {r.summary.exceptions > 0 ? (
                <span className="inline-flex items-center gap-1.5"><span className="h-1.5 w-1.5 rounded-full bg-pulse" aria-hidden />{r.summary.exceptions}</span>
              ) : (
                0
              )}
            </Td>
            <Td numeric>{r.summary.unsettled?.length ?? 0}</Td>
            <Td numeric>{money(r.summary.net_to_bank)}</Td>
            <Td muted>{dateTime(r.created_at)}</Td>
          </tr>
        ))}
      </Table>
      </Card>
    </>
  );
}
