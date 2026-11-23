"use client";

import { useActionState } from "react";
import { createMerchant, type CreateMerchantState } from "@/app/actions";
import { buttonClass, inputClass } from "@/components/ui";

export function CreateMerchantForm() {
  const [state, action, pending] = useActionState<CreateMerchantState, FormData>(createMerchant, null);
  return (
    <div className="space-y-4">
      <form action={action} className="grid gap-4 md:grid-cols-[1fr_8rem_9rem_auto] md:items-end">
        <label>
          <span className="mb-2 block text-caption uppercase text-warm-gray">Name</span>
          <input name="name" required placeholder="Chai Point" className={inputClass} />
        </label>
        <label>
          <span className="mb-2 block text-caption uppercase text-warm-gray">Currency</span>
          <input name="currency" defaultValue="INR" maxLength={3} className={inputClass} />
        </label>
        <label>
          <span className="mb-2 block text-caption uppercase text-warm-gray">Fee (bps)</span>
          <input name="fee_bps" type="number" defaultValue={200} min={0} max={10000} className={inputClass} />
        </label>
        <button type="submit" disabled={pending} className={buttonClass}>
          {pending ? "Creating…" : "Create merchant"}
        </button>
      </form>
      {state?.error && (
        <p className="flex items-center gap-2 text-data">
          <span className="h-1.5 w-1.5 rounded-full bg-pulse" aria-hidden />
          {state.error}
        </p>
      )}
      {state?.key?.secret && (
        <div className="rounded-card border border-pulse p-6">
          <p className="text-caption uppercase">API key for {state.merchant?.name} · shown once</p>
          <code className="mt-3 block break-all font-mono text-[13px]">{state.key.secret}</code>
        </div>
      )}
    </div>
  );
}
