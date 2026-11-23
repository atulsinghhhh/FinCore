"use server";

import { redirect } from "next/navigation";
import { ApiError, rawApi, type Operator } from "@/lib/api";
import { authTarget } from "@/lib/mode";
import { startSession } from "@/lib/session";

export type AcceptState = { error?: string } | null;

export async function acceptInvite(_: AcceptState, form: FormData): Promise<AcceptState> {
  const password = String(form.get("password") ?? "");
  if (password !== String(form.get("confirm") ?? "")) return { error: "Passwords do not match." };
  let op: Operator;
  try {
    op = await rawApi<Operator>(authTarget(), "/v1/admin/invites/accept", {
      method: "POST",
      body: JSON.stringify({ token: String(form.get("token") ?? ""), name: String(form.get("name") ?? ""), password }),
    });
  } catch (e) {
    return { error: e instanceof ApiError ? e.message : "Could not accept the invite." };
  }
  await startSession(op);
  redirect("/");
}
