"use server";

import { refresh } from "next/cache";
import { api, ApiError, type OperatorInvite, type Role } from "@/lib/api";
import { requireAdmin } from "@/lib/session";

export type InviteState = { error?: string; link?: string; invite?: OperatorInvite } | null;

export async function inviteOperator(_: InviteState, form: FormData): Promise<InviteState> {
  await requireAdmin();
  try {
    const res = await api<{ invite: OperatorInvite; token: string }>("/v1/admin/invites", {
      method: "POST",
      body: JSON.stringify({ email: String(form.get("email") ?? ""), role: String(form.get("role") ?? "viewer") }),
    });
    const base = process.env.DASHBOARD_URL ?? "http://localhost:3100";
    refresh();
    return { invite: res.invite, link: `${base}/invite/${res.token}` };
  } catch (e) {
    return { error: e instanceof ApiError ? e.message : "Could not create the invite." };
  }
}

export async function revokeInvite(form: FormData) {
  await requireAdmin();
  await api(`/v1/admin/invites/${String(form.get("id"))}/revoke`, { method: "POST" });
  refresh();
}

export async function setAccess(form: FormData) {
  await requireAdmin();
  const role = String(form.get("role")) as Role;
  const status = String(form.get("status"));
  await api(`/v1/admin/operators/${String(form.get("id"))}/access`, { method: "POST", body: JSON.stringify({ role, status }) });
  refresh();
}
