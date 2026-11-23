"use server";

import { refresh } from "next/cache";
import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { liveConfigured, MODE_COOKIE } from "@/lib/mode";
import { endSession, requireAdmin, requireSession } from "@/lib/session";
import { api, ApiError, type APIKey, type Merchant, type SettlementReport } from "@/lib/api";

export type CreateMerchantState = { error?: string; merchant?: Merchant; key?: APIKey } | null;

export async function createMerchant(_: CreateMerchantState, form: FormData): Promise<CreateMerchantState> {
  try {
    await requireAdmin();
    const res = await api<{ merchant: Merchant; api_key: APIKey }>("/v1/admin/merchants", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        name: String(form.get("name") ?? "").trim(),
        currency: String(form.get("currency") ?? "INR"),
        fee_bps: Number(form.get("fee_bps") ?? 200),
      }),
    });
    refresh();
    return { merchant: res.merchant, key: res.api_key };
  } catch (e) {
    return { error: e instanceof ApiError || e instanceof Error ? e.message : "Could not create merchant" };
  }
}

export type ImportState = { error?: string } | null;

export async function importSettlement(_: ImportState, form: FormData): Promise<ImportState> {
  const file = form.get("file");
  try {
    await requireAdmin();
  } catch (e) {
    return { error: (e as Error).message };
  }
  if (!(file instanceof File) || file.size === 0) return { error: "Choose a CSV file" };
  let report: SettlementReport;
  try {
    const params = new URLSearchParams({ provider: String(form.get("provider") ?? "razorpay"), source: file.name });
    report = await api<SettlementReport>(`/v1/admin/settlements?${params}`, {
      method: "POST",
      headers: { "Content-Type": "text/csv" },
      body: await file.text(),
    });
  } catch (e) {
    return { error: e instanceof ApiError ? e.message : "Import failed" };
  }
  redirect(`/settlements/${report.id}`);
}

export async function requeueOutboxEvent(form: FormData) {
  await requireAdmin();
  await api(`/v1/admin/outbox/${Number(form.get("id"))}/requeue`, { method: "POST" });
  refresh();
}

export async function settlePayout(form: FormData) {
  await requireAdmin();
  const id = String(form.get("id"));
  const outcome = form.get("outcome") === "failed" ? "failed" : "paid";
  await api(`/v1/admin/sandbox/payouts/${id}/${outcome}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: outcome === "failed" ? JSON.stringify({ reason: "rejected_in_dashboard" }) : undefined,
  });
  refresh();
}

export async function logout() {
  await endSession();
  redirect("/login");
}

/** Switch between the test and live APIs (only if live is configured). */
export async function setMode(form: FormData) {
  await requireSession();
  const mode = form.get("mode") === "live" && liveConfigured() ? "live" : "test";
  (await cookies()).set(MODE_COOKIE, mode, { httpOnly: true, sameSite: "lax", path: "/", maxAge: 60 * 60 * 24 * 365 });
  redirect("/");
}
