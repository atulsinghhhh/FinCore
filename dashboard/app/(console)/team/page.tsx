import { Suspense } from "react";
import { api, type Operator, type OperatorEvent, type OperatorInvite } from "@/lib/api";
import { dateTime } from "@/lib/format";
import { requireSession } from "@/lib/session";
import { Badge, Card, PageHeader, Skeleton, Table, Td, smallButtonClass } from "@/components/ui";
import { InviteForm } from "./invite-form";
import { revokeInvite, setAccess } from "./actions";

export const metadata = { title: "Team" };

export default function TeamPage() {
  return (
    <>
      <PageHeader
        title="Team"
        description="Operators join by invitation. Admins can change anything; read-only members can see everything and change nothing."
      />
      <Suspense fallback={<Skeleton rows={10} />}>
        <Team />
      </Suspense>
    </>
  );
}

const roleLabel = (r: string) => (r === "admin" ? "admin" : "read-only");

async function Team() {
  const me = await requireSession();
  const isAdmin = me.role === "admin";
  const [{ operators, invites }, events] = await Promise.all([
    api<{ operators: Operator[]; invites: OperatorInvite[] }>("/v1/admin/operators"),
    api<{ data: OperatorEvent[] }>("/v1/admin/operators/events"),
  ]);

  return (
    <div className="space-y-6">
      {isAdmin ? (
        <Card title="Invite a teammate">
          <InviteForm />
        </Card>
      ) : (
        <p className="flex items-center gap-2 rounded-card border border-mist p-6 text-body">
          <span className="h-1.5 w-1.5 rounded-full bg-pulse" aria-hidden />
          You have read-only access. Ask an admin to invite people or change roles.
        </p>
      )}

      <Card title={`Members · ${operators.length}`}>
        <Table head={["Name", "Email", "Role", "Status", "Last sign-in", ""]} empty={operators.length === 0}>
          {operators.map((o) => {
            const self = o.id === me.id;
            return (
              <tr key={o.id}>
                <Td>{o.name}{self && <span className="text-warm-gray"> (you)</span>}</Td>
                <Td muted>{o.email}</Td>
                <Td><Badge tone={o.role === "admin" ? "settled" : "moving"}>{roleLabel(o.role)}</Badge></Td>
                <Td><Badge tone={o.status === "active" ? "neutral" : "attention"}>{o.status}</Badge></Td>
                <Td muted>{o.last_login_at ? dateTime(o.last_login_at) : "never"}</Td>
                <Td>
                  {isAdmin && !self && (
                    <div className="flex justify-end gap-2">
                      <form action={setAccess}>
                        <input type="hidden" name="id" value={o.id} />
                        <input type="hidden" name="status" value={o.status} />
                        <input type="hidden" name="role" value={o.role === "admin" ? "viewer" : "admin"} />
                        <button className={smallButtonClass}>{o.role === "admin" ? "Make read-only" : "Make admin"}</button>
                      </form>
                      <form action={setAccess}>
                        <input type="hidden" name="id" value={o.id} />
                        <input type="hidden" name="role" value={o.role} />
                        <input type="hidden" name="status" value={o.status === "active" ? "disabled" : "active"} />
                        <button className={smallButtonClass}>{o.status === "active" ? "Disable" : "Enable"}</button>
                      </form>
                    </div>
                  )}
                </Td>
              </tr>
            );
          })}
        </Table>
      </Card>

      <Card title={`Pending invites · ${invites.length}`}>
        <Table head={["Email", "Role", "Invited by", "Expires", ""]} empty={invites.length === 0}>
          {invites.map((i) => {
            const expired = new Date(i.expires_at) < new Date();
            return (
              <tr key={i.id}>
                <Td>{i.email}</Td>
                <Td><Badge tone={i.role === "admin" ? "settled" : "moving"}>{roleLabel(i.role)}</Badge></Td>
                <Td muted>{i.invited_by_name ?? "CLI"}</Td>
                <Td muted>{expired ? <Badge tone="attention">expired</Badge> : dateTime(i.expires_at)}</Td>
                <Td>
                  {isAdmin && (
                    <form action={revokeInvite} className="flex justify-end">
                      <input type="hidden" name="id" value={i.id} />
                      <button className={smallButtonClass}>Revoke</button>
                    </form>
                  )}
                </Td>
              </tr>
            );
          })}
        </Table>
      </Card>

      <Card title="Activity">
        <Table head={["When", "Who", "Action", "Target", "Detail"]} empty={events.data.length === 0}>
          {events.data.map((e) => (
            <tr key={e.id}>
              <Td muted>{dateTime(e.created_at)}</Td>
              <Td>{e.actor_name ?? "system"}</Td>
              <Td mono>{e.action}</Td>
              <Td muted>{e.target}</Td>
              <Td muted>{e.detail}</Td>
            </tr>
          ))}
        </Table>
      </Card>
    </div>
  );
}
