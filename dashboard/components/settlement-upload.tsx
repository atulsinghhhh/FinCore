"use client";

import { useActionState } from "react";
import { importSettlement, type ImportState } from "@/app/actions";
import { buttonClass, inputClass } from "@/components/ui";

export function SettlementUpload() {
  const [state, action, pending] = useActionState<ImportState, FormData>(importSettlement, null);
  return (
    <form action={action} className="space-y-4">
      <div className="grid gap-4 md:grid-cols-[11rem_1fr_auto] md:items-end">
        <label>
          <span className="mb-2 block text-caption uppercase text-warm-gray">Provider</span>
          <select name="provider" className={inputClass} defaultValue="razorpay">
            <option value="razorpay">Razorpay</option>
            <option value="sandbox">Sandbox</option>
          </select>
        </label>
        <label>
          <span className="mb-2 block text-caption uppercase text-warm-gray">Settlement file (CSV)</span>
          <input name="file" type="file" accept=".csv,text/csv" required className={`${inputClass} py-3 file:mr-4 file:rounded-pill file:border file:border-ink file:bg-white file:px-4 file:py-1 file:text-data`} />
        </label>
        <button type="submit" disabled={pending} className={buttonClass}>
          {pending ? "Reconciling…" : "Import & reconcile"}
        </button>
      </div>
      <p className="text-data text-warm-gray">
        Columns: <code className="font-mono text-[13px] text-ink">settlement_id, entity_type, entity_id, amount, fee, tax, currency, settled_at</code> — amounts in paise. Importing the same file twice is safe.
      </p>
      {state?.error && (
        <p className="flex items-center gap-2 text-data">
          <span className="h-1.5 w-1.5 rounded-full bg-pulse" aria-hidden />
          {state.error}
        </p>
      )}
    </form>
  );
}
