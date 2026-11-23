"use client";

import { useActionState } from "react";
import { acceptInvite, type AcceptState } from "./actions";
import { buttonClass, inputClass } from "@/components/ui";

export function AcceptForm({ token, email }: { token: string; email: string }) {
  const [state, action, pending] = useActionState<AcceptState, FormData>(acceptInvite, null);
  return (
    <form action={action} className="space-y-5">
      <input type="hidden" name="token" value={token} />
      <input type="email" autoComplete="username" value={email} readOnly hidden />
      <label className="block">
        <span className="mb-2 block text-caption uppercase text-warm-gray">Your name</span>
        <input name="name" required autoFocus maxLength={100} className={inputClass} placeholder="Asha Rao" />
      </label>
      <label className="block">
        <span className="mb-2 block text-caption uppercase text-warm-gray">Password · at least 12 characters</span>
        <input name="password" type="password" autoComplete="new-password" minLength={12} required className={inputClass} />
      </label>
      <label className="block">
        <span className="mb-2 block text-caption uppercase text-warm-gray">Confirm password</span>
        <input name="confirm" type="password" autoComplete="new-password" minLength={12} required className={inputClass} />
      </label>
      {state?.error && (
        <p role="alert" className="flex items-center gap-2 text-data">
          <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-pulse" aria-hidden />
          {state.error}
        </p>
      )}
      <button type="submit" disabled={pending} className={`${buttonClass} w-full`}>
        {pending ? "Creating account…" : "Join FinCore"}
      </button>
    </form>
  );
}
