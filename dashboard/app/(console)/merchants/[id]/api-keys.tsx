"use client";

import { useActionState, useState } from "react";
import { issueApiKey, revokeApiKey, type IssueKeyState, type RevokeKeyState } from "./key-actions";
import { buttonClass, smallButtonClass } from "@/components/ui";

/** Issue a new key; its secret is shown here exactly once. */
export function IssueKey({ merchantId }: { merchantId: string }) {
  const [state, action, pending] = useActionState<IssueKeyState, FormData>(issueApiKey, null);
  const [copied, setCopied] = useState(false);
  return (
    <div className="space-y-4">
      <form action={action} className="flex flex-wrap items-center gap-4">
        <input type="hidden" name="merchant_id" value={merchantId} />
        <button type="submit" disabled={pending} className={buttonClass}>
          {pending ? "Issuing…" : "Issue new key"}
        </button>
        <p className="max-w-md text-data text-warm-gray">
          To rotate: issue a new key, switch the merchant&apos;s integration to it, then revoke the old one.
        </p>
      </form>
      {state?.error && <Alert>{state.error}</Alert>}
      {state?.key?.secret && (
        <div className="rounded-card border border-pulse p-6">
          <p className="text-caption uppercase">New secret key · shown once · copy it now</p>
          <div className="mt-3 flex flex-wrap items-center gap-3">
            <code className="min-w-0 flex-1 break-all font-mono text-[13px]">{state.key.secret}</code>
            <button
              type="button"
              className={smallButtonClass}
              onClick={async () => {
                await navigator.clipboard.writeText(state.key!.secret!);
                setCopied(true);
              }}
            >
              {copied ? "Copied" : "Copy key"}
            </button>
          </div>
          <p className="mt-3 text-data text-warm-gray">
            FinCore stores only a hash of this key, so it cannot be shown again. Share it with the merchant&apos;s developers over a secure channel.
          </p>
        </div>
      )}
    </div>
  );
}

/** Two-step revoke: revoking breaks any integration still using the key. */
export function RevokeKey({ merchantId, keyId, prefix, lastActive }: { merchantId: string; keyId: string; prefix: string; lastActive: boolean }) {
  const [confirming, setConfirming] = useState(false);
  const [state, action, pending] = useActionState<RevokeKeyState, FormData>(revokeApiKey, null);
  if (!confirming) {
    return (
      <button type="button" className={smallButtonClass} onClick={() => setConfirming(true)}>
        Revoke
      </button>
    );
  }
  return (
    <form action={action} className="flex flex-col items-end gap-2">
      <input type="hidden" name="merchant_id" value={merchantId} />
      <input type="hidden" name="key_id" value={keyId} />
      <p className="max-w-64 text-right text-data whitespace-normal">
        {lastActive
          ? `${prefix}… is the only active key — the merchant's integration will stop working.`
          : `Requests using ${prefix}… will be rejected immediately.`}
      </p>
      <div className="flex gap-2">
        <button type="button" className={smallButtonClass} onClick={() => setConfirming(false)} disabled={pending}>
          Cancel
        </button>
        <button type="submit" disabled={pending} className="inline-flex items-center gap-1.5 rounded-pill border border-pulse px-3 py-1 text-caption uppercase">
          <span className="h-1.5 w-1.5 rounded-full bg-pulse" aria-hidden />
          {pending ? "Revoking…" : "Confirm revoke"}
        </button>
      </div>
      {state?.error && <Alert>{state.error}</Alert>}
    </form>
  );
}

function Alert({ children }: { children: React.ReactNode }) {
  return (
    <p role="alert" className="flex items-center gap-2 text-data">
      <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-pulse" aria-hidden />
      {children}
    </p>
  );
}
