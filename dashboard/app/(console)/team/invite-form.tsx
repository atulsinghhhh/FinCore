"use client";

import { useActionState, useState } from "react";
import { inviteOperator, type InviteState } from "./actions";
import { buttonClass, inputClass, smallButtonClass } from "@/components/ui";

export function InviteForm() {
  const [state, action, pending] = useActionState<InviteState, FormData>(inviteOperator, null);
  const [copied, setCopied] = useState(false);
  return (
    <div className="space-y-4">
      <form action={action} className="grid gap-4 md:grid-cols-[1fr_12rem_auto] md:items-end">
        <label>
          <span className="mb-2 block text-caption uppercase text-warm-gray">Email</span>
          <input name="email" type="email" required placeholder="teammate@company.com" className={inputClass} />
        </label>
        <label>
          <span className="mb-2 block text-caption uppercase text-warm-gray">Role</span>
          <select name="role" defaultValue="viewer" className={inputClass}>
            <option value="viewer">Read-only</option>
            <option value="admin">Admin</option>
          </select>
        </label>
        <button type="submit" disabled={pending} className={buttonClass}>
          {pending ? "Inviting…" : "Send invite"}
        </button>
      </form>
      {state?.error && (
        <p role="alert" className="flex items-center gap-2 text-data">
          <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-pulse" aria-hidden />
          {state.error}
        </p>
      )}
      {state?.link && (
        <div className="rounded-card border border-pulse p-6">
          <p className="text-caption uppercase">Invite link for {state.invite?.email} · shown once · expires in 72 hours</p>
          <div className="mt-3 flex flex-wrap items-center gap-3">
            <code className="min-w-0 flex-1 break-all font-mono text-[13px]">{state.link}</code>
            <button
              type="button"
              className={smallButtonClass}
              onClick={async () => {
                await navigator.clipboard.writeText(state.link!);
                setCopied(true);
              }}
            >
              {copied ? "Copied" : "Copy link"}
            </button>
          </div>
          <p className="mt-3 text-data text-warm-gray">Send it to them directly. Anyone with the link can join with this role, so share it privately.</p>
        </div>
      )}
    </div>
  );
}
