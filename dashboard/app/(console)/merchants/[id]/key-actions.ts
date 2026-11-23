"use server";

import { refresh } from "next/cache";
import { api, ApiError, type APIKey } from "@/lib/api";
import { requireAdmin } from "@/lib/session";

export type IssueKeyState = { error?: string; key?: APIKey } | null;

/** Issues an additional key; the secret is returned once and never stored. */
export async function issueApiKey(_: IssueKeyState, form: FormData): Promise<IssueKeyState> {
  try {
    await requireAdmin();
    const key = await api<APIKey>(`/v1/admin/merchants/${String(form.get("merchant_id"))}/api-keys`, { method: "POST" });
    refresh();
    return { key };
  } catch (e) {
    return { error: e instanceof ApiError || e instanceof Error ? e.message : "Could not issue a key." };
  }
}

export type RevokeKeyState = { error?: string; done?: boolean } | null;

export async function revokeApiKey(_: RevokeKeyState, form: FormData): Promise<RevokeKeyState> {
  try {
    await requireAdmin();
    await api(`/v1/admin/merchants/${String(form.get("merchant_id"))}/api-keys/${String(form.get("key_id"))}/revoke`, { method: "POST" });
    refresh();
    return { done: true };
  } catch (e) {
    return { error: e instanceof ApiError || e instanceof Error ? e.message : "Could not revoke the key." };
  }
}
