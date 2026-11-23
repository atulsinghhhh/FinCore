# Ledger-Backed Payment Processing System

A production-grade payment platform built on a **double-entry ledger** — the same accounting model used by Stripe, Razorpay, Wise, Modern Treasury, and banks.

---

## 1. What It Is

A payment system moves money: a customer pays, a merchant gets paid, the platform takes a fee, and sometimes there are refunds.

A **ledger-backed** system records every money movement in a **double-entry ledger**. Balances are never stored as a number you overwrite (`balance = balance - 100`). They are always **calculated from an unchangeable history of entries**.

### The core idea: double-entry

Every transaction has at least two entries, and **debits always equal credits**. Money is never created or destroyed. It only moves between accounts.

**Example:** a customer pays ₹1,000 to a merchant, and the platform keeps a 2% fee.

| Account | Debit | Credit |
|---|---|---|
| Customer funds (via payment provider) | ₹1,000 | |
| Merchant payable | | ₹980 |
| Platform fee revenue | | ₹20 |
| **Total** | **₹1,000** | **₹1,000** ✅ |

Later, when the merchant is paid out to their bank:

| Account | Debit | Credit |
|---|---|---|
| Merchant payable | ₹980 | |
| Bank / settlement account | | ₹980 |

### Why this matters

- **You can't lose money silently.** If entries don't balance, the write is rejected.
- **You get an audit trail for free.** Every rupee can be traced back to its source.
- **Mistakes are reversed, never edited.** A new entry cancels the bad one, so history is never rewritten.
- **You can rebuild any balance at any point in time.** Regulators and auditors require this.

---

## 2. Core Concepts

| Concept | What it means |
|---|---|
| **Accounts** | Buckets of money: customer wallet, merchant payable, fees, settlement, refunds. Each has a type: asset, liability, revenue or expense. |
| **Journal entries / transactions** | One atomic, balanced group of postings. Either all of it is written or none of it is. |
| **Postings** | Single debit or credit lines inside a transaction. |
| **Immutability** | Entries are append-only, with no UPDATE or DELETE. Enforced at the database level. |
| **Money as integers** | Store paise, not floats (`₹10.50` → `1050`). Use `bigint` plus a currency code. |
| **Idempotency keys** | Client retries must not charge twice. Every write API takes an `Idempotency-Key` header. |
| **Payment state machine** | `created → authorized → captured → settled`, with branches to `failed` and `refunded`. Only valid transitions are allowed. |
| **Holds / authorizations** | Reserve funds before capturing them, like a card authorization. Track pending vs. available balances. |
| **Concurrency safety** | Two simultaneous withdrawals must not overdraw an account. Use row locks (`SELECT … FOR UPDATE`) or optimistic versioning. |
| **Payment provider webhooks** | Razorpay/Stripe report results asynchronously. Verify the signature, dedupe the event, process it exactly once. |
| **Outbox pattern** | Ledger writes and outgoing events are saved in the same DB transaction, so they never get out of sync. |
| **Reconciliation** | A daily job compares the ledger against the provider's or bank's settlement report and flags mismatches. |

---

## 3. Architecture

```
            ┌──────────────┐
 Client ───▶│  API Gateway │  auth, rate-limit, idempotency
            └──────┬───────┘
                   ▼
   ┌──────────────────────────────┐
   │      Payments Service        │  state machine, orchestration
   └──────┬───────────────┬───────┘
          ▼               ▼
 ┌────────────────┐  ┌────────────────────┐
 │ Ledger Service │  │ Payment provider   │  Razorpay/Stripe (test mode)
 │ (double-entry) │  │ adapter + webhooks │
 └───────┬────────┘  └─────────┬──────────┘
         ▼                     ▼
   ┌───────────┐        ┌─────────────┐
   │ PostgreSQL│◀─outbox─│  Go Worker  │  events, payouts, retries
   └───────────┘        └─────────────┘
         ▲
   ┌─────┴─────────────┐
   │ Reconciliation job│  ledger vs provider/bank reports
   └───────────────────┘
         ▲
   ┌─────┴──────┐
   │ Admin UI   │  balances, transactions, audit trail
   └────────────┘
```

### Tech stack

| Layer | Choice | Why |
|---|---|---|
| Language | **Go 1.26** | Fast, simple concurrency, explicit error handling — widely used in fintech backends |
| HTTP router | **chi** | Lightweight, idiomatic `net/http`, good middleware story (auth, idempotency, request IDs) |
| Database | **PostgreSQL 17** (Docker image `postgres:17-alpine`) | ACID transactions, `SELECT … FOR UPDATE`, triggers enforcing balanced entries |
| DB driver | **pgx v5** | Fastest Postgres driver for Go, full control over transactions and isolation levels |
| Queries | **sqlc** | Type-safe Go generated from plain SQL — no ORM hiding the locks |
| Migrations | **goose** | Versioned SQL migrations |
| In-memory store | **Dragonfly** (Docker image `docker.dragonflydb.io/dragonflydb/dragonfly`) | Redis-compatible, multi-threaded; used for idempotency keys, rate limiting, distributed locks, webhook dedupe, balance cache |
| Dragonfly client | **go-redis v9** | Works unchanged against Dragonfly |
| Background jobs | **Postgres outbox + Go worker** (`SKIP LOCKED`) | Events are written in the same DB transaction as ledger entries — no lost or phantom events |
| Payment provider | Razorpay test mode (optional Stripe adapter) | UPI, cards, payouts for India |
| Validation / config | Explicit Go validation, env-based config | Small surface, no reflection magic |
| Logging / tracing | `log/slog`, OpenTelemetry, Prometheus + Grafana | Structured logs, traces, metrics |
| Testing | `testing`, testify, **testcontainers-go**, `rapid` (property-based) | Real Postgres in tests; prove ledger invariants |
| Load testing | **k6** | Concurrency / double-spend proof |
| API docs | OpenAPI 3.1 (`internal/httpapi/openapi.yaml`, served at `/openapi.yaml`) | A test fails if any route is undocumented |
| Dashboard | Next.js + shadcn/ui | Admin UI for balances, transactions, audit trail |
| Infrastructure | Docker Compose locally, GitHub Actions CI | One command to run everything |

### What lives where

| Data | Store | Reason |
|---|---|---|
| Accounts, transactions, postings, payments, outbox | PostgreSQL | Source of truth — must be durable and ACID |
| Idempotency keys + cached responses (24h TTL) | Dragonfly | Fast lookup on every write request |
| Rate-limit counters | Dragonfly | High-frequency, short-lived |
| Webhook event dedupe (`SETNX event_id`) | Dragonfly (+ unique constraint in Postgres as backstop) | Fast first check, durable second check |
| Balance cache | Dragonfly | Read-heavy; always rebuildable from the ledger |

> Rule: Dragonfly is a **speed layer**, never the source of truth. If Dragonfly is wiped, no money data is lost.

### Running infrastructure locally

```bash
cp .env.example .env
docker compose up -d          # postgres:17-alpine on :5434, dragonfly on :6379
make migrate                  # apply schema
make test                     # full suite (throwaway postgres via testcontainers)
make demo                     # ₹1,000 payment example against the local db
docker compose ps             # both should be "healthy"
```

### Project layout

```
fincore/
├── cmd/
│   ├── api/              # HTTP server entrypoint
│   └── worker/           # outbox, payouts, reconciliation
├── internal/
│   ├── ledger/           # pure double-entry core (no HTTP, no framework)
│   ├── payments/         # state machine + orchestration
│   ├── provider/         # Razorpay / Stripe adapters + webhooks
│   ├── idempotency/      # Dragonfly-backed middleware
│   ├── store/            # sqlc-generated queries
│   └── platform/         # config, logging, db + dragonfly clients
├── db/
│   ├── migrations/       # goose SQL migrations
│   └── queries/          # sqlc .sql files
├── dashboard/            # Next.js admin UI
├── docs/adr/             # architecture decision records
├── docker-compose.yml
└── .env.example
```

---

## 4. Build Plan

### Phase 1 — Ledger core (most important) ✅
- [x] Schema: `accounts`, `transactions`, `postings`, `balances` (cached projection)
- [x] DB constraint/trigger that rejects unbalanced transactions
- [x] Append-only enforcement, integer money, currency on every posting
- [x] Point-in-time balance queries

### Phase 2 — Payments API ✅
- [x] `POST /payments`, `/capture`, `/refund`, `/payouts`
- [x] Idempotency-key middleware (stored request hash + response)
- [x] State machine with transition guards

### Phase 3 — Payment provider integration ✅
- [x] Adapter interface so Razorpay and Stripe are swappable
- [x] Webhook endpoint: signature verification, event dedupe, retry handling

### Phase 4 — Reliability ✅
- [x] Outbox + worker, retries with exponential backoff, dead-letter queue
- [x] Concurrency tests: 100 parallel debits on one account never go below zero
- [x] Dragonfly: idempotency keys
- [x] Dragonfly: webhook dedupe (fast path)
- [x] Dragonfly: rate limiting (balance cache not needed: cached balances live in Postgres, updated in the same transaction)

### Phase 5 — Reconciliation & reporting ✅
- [x] Import settlement file, match against ledger, report mismatches
- [x] Trial balance report: total debits = total credits system-wide

### Phase 6 — Production polish ✅
- [x] Auth (API keys for merchants, JWT for dashboard) and RBAC
- [x] Structured logs, metrics, tracing, OpenAPI spec
- [x] Admin dashboard: transactions, balances, audit-trail drill-down

---

## 5. What Makes It Stand Out to Fintech Recruiters

1. **Invariant tests** — property-based tests proving that after thousands of random operations, the ledger always balances.
2. **Concurrency proof** — goroutine-based tests plus a k6 load test showing no double-spends and no negative balances under parallel load.
3. **Idempotency demo** — the same request sent 10 times charges once.
4. **Architecture Decision Records (ADRs)** — short docs on why double-entry, why Postgres, why the outbox pattern.
5. **README with sequence diagrams** for the payment, refund and payout flows.
6. **Failure handling** — what happens when a webhook arrives twice, arrives before the API response, or never arrives (reconciliation catches it).

---

## 6. API Reference

All amounts are integers in minor units (paise). Every `POST` requires an `Idempotency-Key` header.

| Method | Path | Purpose |
|---|---|---|
| POST | `/v1/merchants` | Create a merchant (`name`, `currency`, `fee_bps`, default 200 = 2%) |
| GET | `/v1/merchants/{id}/balance` | Available balance and pending payouts |
| POST | `/v1/payments` | Create + authorize (+ capture if `capture_method` is `automatic`) |
| GET | `/v1/payments/{id}` | Payment state |
| GET | `/v1/payments/{id}/events` | Append-only audit trail of state changes |
| POST | `/v1/payments/{id}/capture` | Capture an authorized payment (optional partial `amount`) |
| POST | `/v1/payments/{id}/cancel` | Void an uncaptured authorization |
| POST | `/v1/payments/{id}/refunds` | Full or partial refund (fee returned pro rata) |
| POST | `/v1/payments/{id}/confirm` | Forward Razorpay Checkout's signed result (`razorpay_payment_id`, `razorpay_order_id`, `razorpay_signature`) |
| POST | `/webhooks/razorpay` | Razorpay webhooks (HMAC-verified, deduplicated by `X-Razorpay-Event-Id`) |
| GET | `/dev/checkout/{id}` | Dev-only page that opens Razorpay Checkout for a created payment |
| POST | `/v1/payouts` | Move available balance into transit to the merchant's bank |
| POST | `/v1/sandbox/payouts/{id}/paid` \| `failed` | Simulate the bank's answer (sandbox only) |
| GET | `/healthz` | Postgres + Dragonfly health |

**Payment states:** `created → authorized → captured → partially_refunded → refunded`, with `created → failed` and `authorized → canceled`. Enforced in Go *and* by a Postgres trigger reading the `status_transitions` table.

**Sandbox payment methods:** `pm_card_success`, `pm_card_declined`, `pm_card_insufficient_funds`, `pm_card_provider_error`.

**Error format:** `{"error": {"code": "insufficient_funds", "message": "..."}}` — codes: `invalid_request`, `invalid_json`, `not_found`, `invalid_state` (409), `insufficient_funds` (422), `idempotency_key_reused` (422), `request_in_progress` (409), `provider_unavailable` (502).

### Ledger postings per operation

| Operation | Debit | Credit |
|---|---|---|
| Capture | psp_clearing (amount) | merchant payable (amount − fee), fee_revenue (fee) |
| Refund | merchant payable (amount − fee share), fee_revenue (fee share) | psp_clearing (amount) |
| Payout created | merchant payable | payouts_in_transit |
| Payout paid | payouts_in_transit | psp_clearing |
| Payout failed | reversal of the hold | |

### Razorpay flow (Phase 3)

```
client ──POST /v1/payments {provider:"razorpay"}──▶ FinCore ──POST /orders──▶ Razorpay
       ◀── 201 status=created, next_action{order_id,key_id} ──
customer pays in Razorpay Checkout (browser)
browser ──POST /v1/payments/{id}/confirm {signed result}──▶ FinCore ──GET /payments/{pay_id}──▶ Razorpay
Razorpay ──POST /webhooks/razorpay (signed)──▶ FinCore          (either may arrive first, or twice)
FinCore: authorized → capture (ledger first, then Razorpay) → captured
```

- Checkout and webhook signatures are verified with HMAC-SHA256 in constant time; status and amount are always re-fetched from Razorpay, never trusted from the browser.
- A failed attempt keeps the payment `created` (the customer can retry the same order); `failure_reason` records the last error.
- Webhooks: Dragonfly answers duplicates fast; `webhook_events (provider, event_id)` in Postgres is the durable dedupe. Transient failures return 500 so Razorpay retries; unprocessable events return 200 and stay in `webhook_events.last_error` for review.
- Refunds may settle later (`pending` → `succeeded`); `refund.failed` reverses the ledger posting and makes the amount refundable again.

**Try it locally:** create a payment with `"provider":"razorpay"`, open `http://localhost:8080/dev/checkout/<payment_id>`, pay with test card `4111 1111 1111 1111` (any future expiry, any CVV).

**Webhooks locally:** expose port 8080 (`cloudflared tunnel --url http://localhost:8080` or `ngrok http 8080`), then in Razorpay Dashboard → Webhooks add `<public-url>/webhooks/razorpay` with the secret from `RAZORPAY_WEBHOOK_SECRET` and events `payment.authorized`, `payment.captured`, `payment.failed`, `refund.processed`, `refund.failed`.

---

## 7. Status

All six phases are complete. See `README.md` for how to run everything, `docs/adr/` for the design decisions, and `internal/httpapi/openapi.yaml` for the full API.
