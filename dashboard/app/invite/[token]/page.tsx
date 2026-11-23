import { Suspense } from "react";
import Link from "next/link";
import { ApiError, rawApi, type OperatorInvite } from "@/lib/api";
import { authTarget } from "@/lib/mode";
import { dateTime } from "@/lib/format";
import { Skeleton } from "@/components/ui";
import { ThemeToggle } from "@/components/theme-toggle";
import { AcceptForm } from "./accept-form";

export const metadata = { title: "Accept invite" };

export default function InvitePage({ params }: PageProps<"/invite/[token]">) {
  return (
    <div className="flex min-h-screen flex-col px-6 py-10 md:px-16">
      <div className="flex items-center justify-between">
        <span className="flex items-center gap-2.5">
          <span className="h-3.5 w-3.5 rounded-full bg-pulse" aria-hidden />
          <span className="text-heading-sm">FinCore</span>
        </span>
        <ThemeToggle />
      </div>
      <div className="my-auto w-full max-w-sm self-center py-12">
        <Suspense fallback={<Skeleton rows={6} />}>
          <Invite params={params} />
        </Suspense>
      </div>
    </div>
  );
}

async function Invite({ params }: { params: PageProps<"/invite/[token]">["params"] }) {
  const { token } = await params;
  let invite: OperatorInvite | null = null;
  try {
    invite = await rawApi<OperatorInvite>(authTarget(), "/v1/admin/invites/lookup", { method: "POST", body: JSON.stringify({ token }) });
  } catch (e) {
    if (!(e instanceof ApiError) || e.status !== 410) throw e;
  }
  if (!invite) {
    return (
      <>
        <span className="inline-flex items-center gap-2 rounded-tag border border-pulse px-4 py-1 text-caption uppercase">
          <span className="h-1.5 w-1.5 rounded-full bg-pulse" aria-hidden /> Invite unavailable
        </span>
        <h1 className="mt-6 text-heading">This link no longer works.</h1>
        <p className="mt-4 text-body text-warm-gray">It may have been used, revoked or expired (links last 72 hours). Ask an admin to send a new invite.</p>
        <Link href="/login" className="mt-8 inline-block text-body underline decoration-mist underline-offset-4 hover:decoration-pulse">Go to sign in</Link>
      </>
    );
  }
  return (
    <>
      <span className="inline-flex items-center rounded-tag border border-pulse px-4 py-1 text-caption uppercase">
        Invited as {invite.role === "admin" ? "admin" : "read-only"}
      </span>
      <h1 className="mt-6 text-heading">Join the FinCore console.</h1>
      <p className="mt-3 mb-10 text-body text-warm-gray">
        {invite.invited_by_name ? `${invite.invited_by_name} invited ` : "You've invited "}
        <span className="text-ink">{invite.email}</span>. Set your name and password to continue. Link expires {dateTime(invite.expires_at)}.
      </p>
      <AcceptForm token={token} email={invite.email} />
    </>
  );
}
