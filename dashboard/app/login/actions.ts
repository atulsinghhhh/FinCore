"use server";

import { redirect } from "next/navigation";
import { ApiError, rawApi, type Operator } from "@/lib/api";
import { authTarget } from "@/lib/mode";
import { safeNext, startSession } from "@/lib/session";

export type LoginState = { error?: string } | null;

export async function login(_: LoginState, form: FormData): Promise<LoginState> {
  let op: Operator;
  try {
    op = await rawApi<Operator>(authTarget(), "/v1/admin/operators/login", {
      method: "POST",
      body: JSON.stringify({ email: String(form.get("email") ?? ""), password: String(form.get("password") ?? "") }),
    });
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) {
      await new Promise((r) => setTimeout(r, 400)); // slow down guessing
      return { error: "Incorrect email or password." };
    }
    return { error: e instanceof ApiError ? e.message : "Sign-in is unavailable right now." };
  }
  await startSession(op);
  redirect(safeNext(form.get("next")));
}
