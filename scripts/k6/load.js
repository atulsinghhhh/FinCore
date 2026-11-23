// FinCore load test: concurrency, idempotency and double-spend checks.
//
//   k6 run -e ADMIN_TOKEN=... scripts/k6/load.js        (or: make load-test)
//
// Scenarios run at the same time against one merchant:
//   payments  - new payments with unique idempotency keys
//   retries   - the same request sent 3x with one key: must charge once
//   payouts   - racing payouts that together ask for more than the balance:
//               each must be 201 or 422 insufficient_funds, never a 5xx,
//               and the balance must never go negative
// teardown() then asserts the ledger is balanced and every rupee is
// accounted for.
import http from "k6/http";
import { check, fail } from "k6";
import { Counter } from "k6/metrics";
import { uuidv4 } from "https://jslib.k6.io/k6-utils/1.4.0/index.js";

const BASE = __ENV.BASE_URL || "http://localhost:8080";
const ADMIN = __ENV.ADMIN_TOKEN || fail("ADMIN_TOKEN is required");
const DURATION = __ENV.DURATION || "30s";

const unexpected = new Counter("unexpected_responses");
const duplicateCharges = new Counter("duplicate_charges");
const paymentsCreated = new Counter("payments_created");
const payoutsCreated = new Counter("payouts_created");
const payoutsRejected = new Counter("payouts_rejected_insufficient_funds");

export const options = {
  scenarios: {
    payments: { executor: "constant-vus", vus: 20, duration: DURATION, exec: "payments" },
    retries: { executor: "constant-vus", vus: 10, duration: DURATION, exec: "retries" },
    payouts: { executor: "constant-vus", vus: 20, duration: DURATION, exec: "payouts" },
  },
  thresholds: {
    unexpected_responses: ["count==0"],
    duplicate_charges: ["count==0"],
    checks: ["rate==1"],
    "http_req_duration{scenario:payments}": ["p(95)<500"],
  },
};

const json = (token, key) => ({
  headers: {
    "Content-Type": "application/json",
    Authorization: `Bearer ${token}`,
    ...(key ? { "Idempotency-Key": key } : {}),
  },
});

export function setup() {
  const res = http.post(`${BASE}/v1/admin/merchants`, JSON.stringify({ name: `k6-${Date.now()}`, currency: "INR", fee_bps: 200 }), json(ADMIN, uuidv4()));
  if (res.status !== 201) fail(`create merchant: ${res.status} ${res.body}`);
  const body = res.json();
  return { key: body.api_key.secret, merchantId: body.merchant.id };
}

function pay(key, idemKey, amount) {
  return http.post(`${BASE}/v1/payments`, JSON.stringify({ amount, currency: "INR", payment_method: "pm_card_success" }), json(key, idemKey));
}

export function payments(data) {
  const res = pay(data.key, uuidv4(), 1000 + Math.floor(Math.random() * 9000));
  if (!check(res, { "payment 201": (r) => r.status === 201 && r.json("status") === "captured" })) unexpected.add(1, { body: res.body });
  else paymentsCreated.add(1);
}

export function retries(data) {
  const idemKey = uuidv4();
  const ids = new Set();
  for (let i = 0; i < 3; i++) {
    const res = pay(data.key, idemKey, 5000);
    if (res.status === 409) continue; // still in progress; a real client retries later
    if (!check(res, { "retry 201": (r) => r.status === 201 })) {
      unexpected.add(1);
      continue;
    }
    ids.add(res.json("id"));
  }
  if (ids.size > 1) duplicateCharges.add(1);
  if (ids.size === 1) paymentsCreated.add(1);
}

export function payouts(data) {
  const res = http.post(`${BASE}/v1/payouts`, JSON.stringify({ amount: 500 + Math.floor(Math.random() * 5000) }), json(data.key, uuidv4()));
  const ok = check(res, {
    "payout 201 or 422": (r) => r.status === 201 || (r.status === 422 && r.json("error.code") === "insufficient_funds"),
  });
  if (!ok) unexpected.add(1, { status: String(res.status) });
  else if (res.status === 201) payoutsCreated.add(1);
  else payoutsRejected.add(1);
}

export function teardown(data) {
  const integrity = http.get(`${BASE}/v1/admin/ledger/integrity`, json(ADMIN));
  check(integrity, { "ledger balanced and cache consistent": (r) => r.json("ok") === true });

  const balance = http.get(`${BASE}/v1/admin/merchants/${data.merchantId}/balance`, json(ADMIN)).json();
  check(balance, { "merchant balance never negative": (b) => b.available >= 0 });

  // Every rupee accounted for: net captured = available + paid out.
  let captured = 0;
  let fees = 0;
  const all = (path) => {
    const out = [];
    let cursor = "";
    for (;;) {
      const page = http.get(`${BASE}${path}&limit=200${cursor}`, json(ADMIN)).json();
      out.push(...page.data);
      if (!page.has_more) return out;
      cursor = `&cursor=${encodeURIComponent(page.next_cursor)}`;
    }
  };
  for (const p of all(`/v1/admin/payments?merchant_id=${data.merchantId}`)) {
    captured += p.amount_captured;
    fees += p.fee_amount;
  }
  let paidOut = 0;
  for (const p of all(`/v1/admin/payouts?merchant_id=${data.merchantId}`)) paidOut += p.amount;
  console.log(`captured=${captured} fees=${fees} paid_out=${paidOut} available=${balance.available} pending=${balance.pending_payouts}`);
  check(null, { "captured - fees == available + paid out": () => captured - fees === balance.available + paidOut });
}
