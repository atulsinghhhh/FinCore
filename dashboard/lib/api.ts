import "server-only";

import { currentMode, modeTarget } from "@/lib/mode";
import { requireSession } from "@/lib/session";

// Server-side client for the FinCore admin API. Admin tokens are read from
// the server environment and never reach the browser.

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}

/** Calls the admin API of the current mode (test or live), as the signed-in operator. */
export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const me = await requireSession();
  const mode = await currentMode();
  const target = modeTarget(mode);
  if (!target) throw new ApiError(500, "config", `The ${mode} API is not configured for the dashboard`);
  const headers = new Headers(init.headers);
  headers.set("FinCore-Operator-Id", me.id);
  return rawApi<T>(target, path, { ...init, headers }, mode);
}

/** Calls an API without a session check (sign-in, invites, session validation). */
export async function rawApi<T>(target: { url: string; token: string }, path: string, init: RequestInit = {}, label = "auth"): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set("Authorization", `Bearer ${target.token}`);
  if (init.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  if (init.method && init.method !== "GET") {
    headers.set("Idempotency-Key", headers.get("Idempotency-Key") ?? crypto.randomUUID());
  }
  let res: Response;
  try {
    res = await fetch(`${target.url}${path}`, { ...init, headers });
  } catch {
    throw new ApiError(503, "unreachable", `Cannot reach the FinCore ${label} API at ${target.url}`);
  }
  const body = await res.json().catch(() => null);
  if (!res.ok) {
    throw new ApiError(res.status, body?.error?.code ?? "error", body?.error?.message ?? res.statusText);
  }
  return body as T;
}

/** A newest-first page from a list endpoint. */
export type Page<T> = { data: T[]; next_cursor: string | null; has_more: boolean };

export type Role = "admin" | "viewer";

export type Operator = {
  id: string;
  email: string;
  name: string;
  role: Role;
  status: "active" | "disabled";
  session_version: number;
  created_at: string;
  last_login_at: string | null;
};

export type OperatorInvite = {
  id: string;
  email: string;
  role: Role;
  invited_by_name: string | null;
  expires_at: string;
  created_at: string;
};

export type OperatorEvent = {
  id: number;
  actor_name: string | null;
  action: string;
  target: string;
  detail: string;
  created_at: string;
};

export type PaymentStatus =
  | "created"
  | "authorized"
  | "captured"
  | "partially_refunded"
  | "refunded"
  | "canceled"
  | "failed";

export type Payment = {
  id: string;
  merchant_id: string;
  amount: number;
  currency: string;
  status: PaymentStatus;
  capture_method: string;
  payment_method: string;
  description: string;
  provider: string;
  provider_ref: string | null;
  provider_payment_id: string | null;
  amount_captured: number;
  amount_refunded: number;
  fee_amount: number;
  failure_reason: string | null;
  capture_ledger_txn_id: string | null;
  created_at: string;
  updated_at: string;
};

export type PaymentEvent = {
  id: number;
  from_status: string | null;
  to_status: string;
  amount: number | null;
  ledger_txn_id: string | null;
  note: string;
  created_at: string;
};

export type Refund = {
  id: string;
  amount: number;
  currency: string;
  reason: string;
  status: string;
  provider_refund_id: string | null;
  failure_reason: string | null;
  ledger_txn_id: string;
  reversal_ledger_txn_id: string | null;
  created_at: string;
};

export type PaymentDetail = Payment & { events: PaymentEvent[]; refunds: Refund[] };

export type Merchant = {
  id: string;
  name: string;
  currency: string;
  fee_bps: number;
  payable_account_id: string;
  webhook_url: string | null;
  created_at: string;
};

export type APIKey = {
  id: string;
  prefix: string;
  created_at: string;
  last_used_at: string | null;
  revoked_at: string | null;
  secret?: string;
};

export type MerchantBalance = { currency: string; available: number; pending_payouts: number };

export type Payout = {
  id: string;
  merchant_id: string;
  amount: number;
  currency: string;
  status: string;
  failure_reason: string | null;
  created_at: string;
};

export type Statement = {
  currency: string;
  from: string;
  to: string;
  opening_balance: number;
  closing_balance: number;
  truncated: boolean;
  lines: {
    posting_id: number;
    transaction_id: string;
    description: string;
    direction: "debit" | "credit";
    amount: number;
    effective_at: string;
    balance: number;
  }[];
};

export type Account = {
  id: string;
  code: string;
  name: string;
  type: string;
  currency: string;
  debits_total: number;
  credits_total: number;
  balance: number;
};

export type Integrity = {
  ok: boolean;
  trial_balance: { currency: string; total_debits: number; total_credits: number }[] | null;
  mismatches: { account_id: string }[] | null;
};

export type LedgerTransaction = {
  id: string;
  description: string;
  effective_at: string;
  reverses_id: string | null;
  postings: { id: number; account_id: string; direction: "debit" | "credit"; amount: number; currency: string }[];
};

export type Stats = {
  merchants: number;
  payments: number;
  captured_volume: number;
  refunded_volume: number;
  fee_volume: number;
  failed_payments: number;
  dead_outbox_events: number;
  unprocessed_webhooks: number;
  reconciliation_exceptions: number;
};

export type SettlementLine = {
  line_no: number;
  settlement_id: string;
  entity_type: string;
  entity_id: string;
  amount: number;
  fee: number;
  tax: number;
  currency: string;
  settled_at: string;
  result: string;
  detail: string;
  payment_id: string | null;
};

export type SettlementReport = {
  id: string;
  provider: string;
  source: string;
  created_at: string;
  summary: {
    lines: number;
    matched: number;
    exceptions: number;
    by_result: Record<string, number>;
    gross_settled: number;
    refunded: number;
    fees: number;
    net_to_bank: number;
    unsettled: { payment_id: string; provider_payment_id: string; amount: number; captured_at: string }[];
  };
  lines?: SettlementLine[];
};

export type OutboxEvent = {
  id: number;
  merchant_id: string;
  aggregate_type: string;
  aggregate_id: string;
  event_type: string;
  status: string;
  attempts: number;
  next_attempt_at: string;
  last_error: string | null;
  created_at: string;
  delivered_at: string | null;
};
