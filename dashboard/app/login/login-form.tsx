"use client";

import { useActionState } from "react";
import { login, type LoginState } from "./actions";
import { buttonClass, inputClass } from "@/components/ui";

export function LoginForm({ next }: { next: string }) {
  const [state, action, pending] = useActionState<LoginState, FormData>(login, null);
  return (
    <form action={action} className="space-y-5">
      <input type="hidden" name="next" value={next} />
      <label className="block">
        <span className="mb-2 block text-caption uppercase text-warm-gray">Email</span>
        <input name="email" type="email" autoComplete="username" required autoFocus className={inputClass} placeholder="ops@fincore.dev" />
      </label>
      <label className="block">
        <span className="mb-2 block text-caption uppercase text-warm-gray">Password</span>
        <input name="password" type="password" autoComplete="current-password" required className={inputClass} />
      </label>
      {state?.error && (
        <p role="alert" className="flex items-center gap-2 text-data">
          <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-pulse" aria-hidden />
          {state.error}
        </p>
      )}
      <button type="submit" disabled={pending} className={`${buttonClass} w-full`}>
        {pending ? "Signing in…" : "Sign in"}
      </button>
    </form>
  );
}
